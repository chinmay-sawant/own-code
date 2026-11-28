package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

func newOpenCodeLocalSessionID() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("could not create a local OpenCode session ID")
	}
	return "local-" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func listLocalOpenCodeSessions(ctx context.Context, store *agentstore.Store, workspaceRoot string) ([]opencode.Session, error) {
	if store == nil {
		return nil, errors.New("OpenCode local session store is unavailable")
	}
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	stored, err := store.ListSessions(ctx, root)
	if err != nil {
		return nil, err
	}
	sessions := make([]opencode.Session, 0, len(stored))
	for _, local := range stored {
		if !sameWorkspacePath(local.WorkspaceRoot, root) {
			continue
		}
		sessions = append(sessions, opencode.Session{
			ID:    local.ID,
			Title: local.Title,
			Time: opencode.SessionTime{
				Created: float64(local.CreatedAt.UnixMilli()),
				Updated: float64(local.UpdatedAt.UnixMilli()),
			},
			Location: opencode.LocationRef{Directory: root},
		})
	}
	return sessions, nil
}

// openCodeTranscriptMessage is the safe text-only transcript projection shared
// by session history and the native panel. Unknown, tool, and cross-session
// messages are omitted rather than exposed to a renderer.
type openCodeTranscriptMessage struct {
	ID        string
	Role      string
	Text      string
	Ordinal   int64
	CreatedAt time.Time
	// TurnID, Kind ("text", "tool", "notice") and Note come from the Console
	// runtime projection; legacy rows leave them empty.
	TurnID string
	Kind   string
	Note   string
}

func canonicalWorkspaceRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("workspace root is required for OpenCode session history")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", errors.New("could not resolve the OpenCode workspace root")
	}
	absolute = filepath.Clean(absolute)
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = filepath.Clean(resolved)
	}
	return absolute, nil
}

func sameWorkspacePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	return ctx.Err()
}
