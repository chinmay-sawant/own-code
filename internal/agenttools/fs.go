package agenttools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxReadBytes     = 64 * 1024
	maxListEntries   = 500
	maxSearchResults = 100
	maxSearchFile    = 1 << 20
	maxSearchPattern = 512
	maxPathLen       = 1024
)

// cleanRel lexically validates a workspace-relative path: no absolute paths,
// volumes, NULs, or traversal.
func cleanRel(p string) (string, error) {
	if p == "" {
		p = "."
	}
	if len(p) > maxPathLen || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: bad path", ErrInvalidArgs)
	}
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return "", fmt.Errorf("%w: absolute path %q", ErrOutside, p)
	}
	c := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(p, `\`, "/")))
	if c == ".." || strings.HasPrefix(c, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: traversal in %q", ErrOutside, p)
	}
	for _, part := range strings.Split(c, string(filepath.Separator)) {
		if part == ".." {
			return "", fmt.Errorf("%w: traversal in %q", ErrOutside, p)
		}
	}
	return c, nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// resolve joins rel to the captured root, resolves symlinks, and rechecks
// containment. Called immediately before every filesystem access.
func (r *registry) resolve(rel string) (string, error) {
	real, err := filepath.EvalSymlinks(filepath.Join(r.root, rel))
	if err != nil {
		return "", err
	}
	if !within(r.root, real) {
		return "", fmt.Errorf("%w: %q resolves outside the worktree", ErrOutside, rel)
	}
	return real, nil
}

func schema(s string) json.RawMessage { return json.RawMessage(s) }

type readArgs struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
}

func readFileTool() tool {
	return tool{
		spec: Spec{
			Name:        ToolReadFile,
			Description: "Read a UTF-8 text file inside the workspace (relative path). Returns up to 64 KiB from byte offset.",
			Schema:      schema(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0}},"required":["path"],"additionalProperties":false}`),
		},
		prepare: func(r *registry, raw json.RawMessage) (any, string, func(context.Context) (Output, error), error) {
			var a readArgs
			if err := decodeStrict(raw, &a); err != nil {
				return nil, "", nil, err
			}
			if a.Path == "" || a.Offset < 0 {
				return nil, "", nil, fmt.Errorf("%w: path required, offset >= 0", ErrInvalidArgs)
			}
			rel, err := cleanRel(a.Path)
			if err != nil {
				return nil, "", nil, err
			}
			a.Path = filepath.ToSlash(rel)
			run := func(ctx context.Context) (Output, error) {
				start := time.Now()
				real, err := r.resolve(rel)
				if err != nil {
					return Output{}, err
				}
				f, err := os.Open(real)
				if err != nil {
					return Output{}, err
				}
				defer f.Close()
				st, err := f.Stat()
				if err != nil {
					return Output{}, err
				}
				if !st.Mode().IsRegular() {
					return Output{}, fmt.Errorf("%w: not a regular file", ErrInvalidArgs)
				}
				if _, err := f.Seek(a.Offset, io.SeekStart); err != nil {
					return Output{}, err
				}
				buf, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
				if err != nil {
					return Output{}, err
				}
				trunc := len(buf) > maxReadBytes
				if trunc {
					buf = buf[:maxReadBytes]
				}
				if bytes.IndexByte(buf, 0) >= 0 {
					return Output{}, fmt.Errorf("%w: binary file", ErrInvalidArgs)
				}
				text := strings.ToValidUTF8(string(buf), "�")
				if trunc {
					text += fmt.Sprintf("\n[truncated: read limit %d bytes; continue with offset=%d]", maxReadBytes, a.Offset+int64(len(buf)))
				}
				return Output{Text: text, Truncated: trunc, Duration: time.Since(start)}, nil
			}
			return a, "read " + a.Path, run, nil
		},
	}
}

type listArgs struct {
	Path string `json:"path,omitempty"`
}

func listFilesTool() tool {
	return tool{
		spec: Spec{
			Name:        ToolListFiles,
			Description: "List entries of a directory inside the workspace (relative path, default '.').",
			Schema:      schema(`{"type":"object","properties":{"path":{"type":"string"}},"additionalProperties":false}`),
		},
		prepare: func(r *registry, raw json.RawMessage) (any, string, func(context.Context) (Output, error), error) {
			var a listArgs
			if err := decodeStrict(raw, &a); err != nil {
				return nil, "", nil, err
			}
			rel, err := cleanRel(a.Path)
			if err != nil {
				return nil, "", nil, err
			}
			a.Path = filepath.ToSlash(rel)
			run := func(ctx context.Context) (Output, error) {
				start := time.Now()
				real, err := r.resolve(rel)
				if err != nil {
					return Output{}, err
				}
				ents, err := os.ReadDir(real)
				if err != nil {
					return Output{}, err
				}
				var sb strings.Builder
				trunc := false
				for i, e := range ents {
					if i >= maxListEntries {
						trunc = true
						sb.WriteString(fmt.Sprintf("[truncated: more than %d entries]\n", maxListEntries))
						break
					}
					name := e.Name()
					if e.IsDir() {
						name += "/"
					}
					sb.WriteString(name + "\n")
				}
				return Output{Text: sb.String(), Truncated: trunc, Duration: time.Since(start)}, nil
			}
			return a, "list " + a.Path, run, nil
		},
	}
}

type searchArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

func searchFilesTool() tool {
	return tool{
		spec: Spec{
			Name:        ToolSearchFiles,
			Description: "Search workspace text files for a literal substring. Returns path:line: text matches.",
			Schema:      schema(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`),
		},
		prepare: func(r *registry, raw json.RawMessage) (any, string, func(context.Context) (Output, error), error) {
			var a searchArgs
			if err := decodeStrict(raw, &a); err != nil {
				return nil, "", nil, err
			}
			if a.Pattern == "" || len(a.Pattern) > maxSearchPattern {
				return nil, "", nil, fmt.Errorf("%w: pattern required (max %d bytes)", ErrInvalidArgs, maxSearchPattern)
			}
			rel, err := cleanRel(a.Path)
			if err != nil {
				return nil, "", nil, err
			}
			a.Path = filepath.ToSlash(rel)
			run := func(ctx context.Context) (Output, error) {
				start := time.Now()
				real, err := r.resolve(rel)
				if err != nil {
					return Output{}, err
				}
				var sb strings.Builder
				n := 0
				trunc := false
				errStop := fmt.Errorf("stop")
				walkErr := filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if cerr := ctx.Err(); cerr != nil {
						return cerr
					}
					if d.IsDir() {
						if d.Name() == ".git" {
							return filepath.SkipDir
						}
						return nil
					}
					if !d.Type().IsRegular() { // skips symlinks
						return nil
					}
					if info, err := d.Info(); err != nil || info.Size() > maxSearchFile {
						return nil
					}
					data, err := os.ReadFile(p)
					if err != nil || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
						return nil
					}
					relp, _ := filepath.Rel(r.root, p)
					for i, line := range strings.Split(string(data), "\n") {
						if strings.Contains(line, a.Pattern) {
							if n >= maxSearchResults {
								trunc = true
								return errStop
							}
							if len(line) > 300 {
								line = line[:300] + "..."
							}
							sb.WriteString(fmt.Sprintf("%s:%d: %s\n", filepath.ToSlash(relp), i+1, strings.TrimRight(line, "\r")))
							n++
						}
					}
					return nil
				})
				if walkErr != nil && walkErr != errStop {
					return Output{}, walkErr
				}
				if trunc {
					sb.WriteString(fmt.Sprintf("[truncated: more than %d matches]\n", maxSearchResults))
				}
				return Output{Text: sb.String(), Truncated: trunc, Duration: time.Since(start)}, nil
			}
			return a, "search " + a.Path + " for " + fmt.Sprintf("%q", a.Pattern), run, nil
		},
	}
}
