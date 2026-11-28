package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxSelectedAgentContextFiles = 8
	maxSelectedAgentFileBytes    = 32 << 10
	maxSelectedAgentTotalBytes   = 128 << 10
	maxSelectedAgentFileLines    = 400
	maxSelectedAgentPathBytes    = 4096
)

var errInvalidAgentWorkspace = errors.New("agent context workspace is unavailable")

type agentFileContext struct {
	RelativePath string
	Version      string
	Text         string
	Size         int64
	ModTime      time.Time
	StartLine    int
	EndLine      int
	Truncated    bool
}

type agentContextExclusion struct {
	RelativePath string
	Reason       string
}

type agentFileContextCollection struct {
	WorkspaceRoot   string
	Files           []agentFileContext
	Excluded        []agentContextExclusion
	ExcludedOmitted int
	TotalBytes      int
	Truncated       bool
}

// collectSelectedAgentFiles reads only the caller-selected relative paths.
// ignoredPaths is an optional set of slash-normalized paths already marked
// ignored by the workspace scan. The caller's order is preserved after
// duplicates are removed.
func collectSelectedAgentFiles(workspaceRoot string, selectedPaths []string, ignoredPaths map[string]bool) (agentFileContextCollection, error) {
	rootPath, err := canonicalAgentContextRoot(workspaceRoot)
	if err != nil {
		return agentFileContextCollection{}, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return agentFileContextCollection{}, errInvalidAgentWorkspace
	}
	defer root.Close()

	collection := agentFileContextCollection{WorkspaceRoot: rootPath}
	seen := make(map[string]struct{}, min(len(selectedPaths), maxSelectedAgentContextFiles))
	processCount := min(len(selectedPaths), maxSelectedAgentContextFiles)
	for _, selected := range selectedPaths[:processCount] {
		relative, pathErr := normalizeSelectedAgentContextPath(selected)
		if pathErr != nil {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{
				RelativePath: boundedAgentContextLabel(selected),
				Reason:       "path is outside the selected workspace",
			})
			continue
		}
		if _, duplicate := seen[relative]; duplicate {
			continue
		}
		seen[relative] = struct{}{}
		if containsAgentContextPathComponent(relative, ".git") {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: "repository metadata is excluded by default"})
			continue
		}
		if isSensitiveAgentContextPath(relative) {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: "sensitive paths are excluded by default"})
			continue
		}
		if ignoredPaths[filepath.ToSlash(relative)] {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: "ignored files are excluded by default"})
			continue
		}
		remaining := maxSelectedAgentTotalBytes - collection.TotalBytes
		if remaining <= 0 {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: "context byte limit reached"})
			collection.Truncated = true
			continue
		}
		file, before, openErr := openSelectedAgentContextFile(root, relative)
		if openErr != nil {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: openErr.Error()})
			continue
		}
		limit := min(maxSelectedAgentFileBytes, remaining)
		data, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
		fileInfo, statErr := file.Stat()
		closeErr := file.Close()
		if readErr != nil || statErr != nil || closeErr != nil || !os.SameFile(before, fileInfo) || !fileInfo.Mode().IsRegular() || before.Size() != fileInfo.Size() || !before.ModTime().Equal(fileInfo.ModTime()) {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: "file changed or could not be read safely"})
			continue
		}
		fileTruncated := len(data) > limit || fileInfo.Size() > int64(len(data))
		if len(data) > limit {
			data = data[:limit]
		}
		if !utf8.Valid(data) && fileTruncated {
			if validEnd, ok := incompleteUTF8SuffixStart(data); ok {
				data = data[:validEnd]
			} else {
				collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: "file is not valid UTF-8 text"})
				continue
			}
		}
		if !utf8.Valid(data) {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: "file is not valid UTF-8 text"})
			continue
		}
		data, lineTruncated := limitAgentContextLines(data, maxSelectedAgentFileLines)
		fileTruncated = fileTruncated || lineTruncated
		if len(data) > remaining {
			data = data[:utf8PrefixLength(data, remaining)]
			fileTruncated = true
		}
		if len(data) == 0 && fileInfo.Size() > 0 {
			collection.Excluded = append(collection.Excluded, agentContextExclusion{RelativePath: relative, Reason: "context byte limit reached"})
			collection.Truncated = true
			continue
		}
		text := string(data)
		startLine := 1
		endLine := max(1, bytes.Count(data, []byte{'\n'})+1)
		digest := sha256.New()
		_, _ = io.WriteString(digest, strconv.FormatInt(fileInfo.Size(), 10)+"\x00"+strconv.FormatInt(fileInfo.ModTime().UnixNano(), 10)+"\x00")
		_, _ = digest.Write(data)
		collection.Files = append(collection.Files, agentFileContext{
			RelativePath: relative,
			Version:      hex.EncodeToString(digest.Sum(nil)),
			Text:         text,
			Size:         fileInfo.Size(),
			ModTime:      fileInfo.ModTime(),
			StartLine:    startLine,
			EndLine:      endLine,
			Truncated:    fileTruncated,
		})
		collection.TotalBytes += len(data)
		collection.Truncated = collection.Truncated || fileTruncated
	}
	if omitted := len(selectedPaths) - processCount; omitted > 0 {
		collection.Excluded = append(collection.Excluded, agentContextExclusion{
			Reason: strconv.Itoa(omitted) + " additional selected paths exceeded the file selection limit",
		})
		collection.ExcludedOmitted = omitted
		collection.Truncated = true
	}
	return collection, nil
}

func canonicalAgentContextRoot(workspaceRoot string) (string, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return "", errInvalidAgentWorkspace
	}
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", errInvalidAgentWorkspace
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", errInvalidAgentWorkspace
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errInvalidAgentWorkspace
	}
	return filepath.Clean(root), nil
}

func normalizeSelectedAgentContextPath(selected string) (string, error) {
	if selected == "" || len(selected) > maxSelectedAgentPathBytes {
		return "", errInvalidAgentContextPath
	}
	raw := filepath.FromSlash(selected)
	if filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return "", errInvalidAgentContextPath
	}
	for _, part := range strings.Split(raw, string(filepath.Separator)) {
		if part == ".." {
			return "", errInvalidAgentContextPath
		}
	}
	clean := filepath.Clean(raw)
	if clean == "." || !filepath.IsLocal(clean) {
		return "", errInvalidAgentContextPath
	}
	return filepath.ToSlash(clean), nil
}

func openSelectedAgentContextFile(root *os.Root, relative string) (*os.File, os.FileInfo, error) {
	raw := filepath.FromSlash(relative)
	current := string(filepath.Separator)
	var finalInfo os.FileInfo
	for _, part := range strings.Split(raw, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := root.Lstat(strings.TrimPrefix(current, string(filepath.Separator)))
		if err != nil {
			return nil, nil, errors.New("file is unavailable in the selected workspace")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, errors.New("symlink paths are excluded by default")
		}
		finalInfo = info
	}
	if finalInfo == nil || !finalInfo.Mode().IsRegular() {
		return nil, nil, errors.New("only regular files can be included as context")
	}
	file, err := root.Open(raw)
	if err != nil {
		return nil, nil, errors.New("file is unavailable in the selected workspace")
	}
	return file, finalInfo, nil
}

func limitAgentContextLines(data []byte, maxLines int) ([]byte, bool) {
	lineCount := 1
	for index, value := range data {
		if value != '\n' {
			continue
		}
		lineCount++
		if lineCount > maxLines {
			return data[:index], true
		}
	}
	return data, false
}

func utf8PrefixLength(data []byte, maxBytes int) int {
	end := min(maxBytes, len(data))
	for end > 0 && !utf8.Valid(data[:end]) {
		end--
	}
	return end
}

func incompleteUTF8SuffixStart(data []byte) (int, bool) {
	startAt := max(0, len(data)-utf8.UTFMax+1)
	for start := startAt; start < len(data); start++ {
		if utf8.Valid(data[:start]) && !utf8.FullRune(data[start:]) {
			return start, true
		}
	}
	return 0, false
}

func containsAgentContextPathComponent(path, component string) bool {
	for _, part := range strings.Split(strings.ToLower(filepath.ToSlash(path)), "/") {
		if part == component {
			return true
		}
	}
	return false
}

func boundedAgentContextLabel(value string) string {
	if len(value) > maxSelectedAgentPathBytes {
		bytes := []byte(value)
		return string(bytes[:utf8PrefixLength(bytes, maxSelectedAgentPathBytes)])
	}
	return value
}
