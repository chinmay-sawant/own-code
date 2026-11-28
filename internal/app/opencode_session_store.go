package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
	"github.com/chinmay-sawant/own-code/internal/opencode"
)

const (
	openCodeHistoryPageSize       = 100
	openCodeHistoryMaxPages       = 20
	openCodeHistoryMaxBytes       = 32 << 20
	openCodeMessageMaxBytes       = 1 << 20
	openCodeHistoryMaxItems       = 5000
	openCodeOrdinalPerMillisecond = 1000
)

var errOpenCodeHistoryScope = errors.New("OpenCode session does not belong to the selected workspace and server")

const openCodeConsoleBackendID = "opencode-console"

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
		isConsoleSession := local.ServerID == openCodeConsoleBackendID
		isCachedServerHistory := strings.HasPrefix(local.ServerID, "server-")
		if !isConsoleSession && !isCachedServerHistory {
			continue
		}
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

// openCodeSessionHistory binds persisted history to exactly one workspace and
// one opaque server identity. It never accepts a URL as server identity.
type openCodeSessionHistory struct {
	store         *agentstore.Store
	workspaceRoot string
	serverID      string
}

func newOpenCodeSessionHistory(store *agentstore.Store, workspaceRoot, serverID string) (*openCodeSessionHistory, error) {
	if store == nil {
		return nil, errors.New("OpenCode session history requires a store")
	}
	if strings.TrimSpace(serverID) == "" || len(serverID) > 256 || strings.Contains(serverID, "://") || strings.Contains(serverID, "@") {
		return nil, errors.New("OpenCode session history requires an opaque server ID")
	}
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	return &openCodeSessionHistory{store: store, workspaceRoot: root, serverID: serverID}, nil
}

// PersistSession imports a server session only when its advertised directory
// resolves to the selected workspace. Status is supplied by the caller because
// the session list response does not provide reliable running-state data.
func (h *openCodeSessionHistory) PersistSession(ctx context.Context, remote opencode.Session, status agentstore.SessionStatus) (agentstore.Session, error) {
	if h == nil || h.store == nil {
		return agentstore.Session{}, errors.New("OpenCode session history is unavailable")
	}
	if err := contextError(ctx); err != nil {
		return agentstore.Session{}, err
	}
	if strings.TrimSpace(remote.ID) == "" || len(remote.ID) > 256 {
		return agentstore.Session{}, errors.New("OpenCode session ID is missing or too long")
	}
	remoteRoot, err := canonicalWorkspaceRoot(remote.Location.Directory)
	if err != nil || !sameWorkspacePath(remoteRoot, h.workspaceRoot) {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	created := openCodeSessionTime(remote.Time.Created)
	updated := openCodeSessionTime(remote.Time.Updated)
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if updated.IsZero() {
		updated = created
	}
	stored, err := h.store.CreateSession(ctx, agentstore.Session{
		ID:                stableHistoryID("session", h.serverID, remote.ID),
		WorkspaceRoot:     h.workspaceRoot,
		ServerID:          h.serverID,
		OpenCodeSessionID: remote.ID,
		Title:             remote.Title,
		Status:            status,
		CreatedAt:         created,
		UpdatedAt:         updated,
	})
	if err != nil {
		return agentstore.Session{}, err
	}
	stored.Title = remote.Title
	stored.Status = status
	stored.UpdatedAt = updated
	return h.store.UpdateSessionMetadata(ctx, stored)
}

// ListSessions returns cached history for this workspace and server only.
func (h *openCodeSessionHistory) ListSessions(ctx context.Context) ([]agentstore.Session, error) {
	if h == nil || h.store == nil {
		return nil, errors.New("OpenCode session history is unavailable")
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	sessions, err := h.store.ListSessions(ctx, h.workspaceRoot)
	if err != nil {
		return nil, err
	}
	filtered := make([]agentstore.Session, 0, len(sessions))
	for _, session := range sessions {
		if session.ServerID == h.serverID && sameWorkspacePath(session.WorkspaceRoot, h.workspaceRoot) {
			filtered = append(filtered, session)
		}
	}
	return filtered, nil
}

// LoadMessages reads cached text transcript after verifying that the local
// session belongs to this history scope.
func (h *openCodeSessionHistory) LoadMessages(ctx context.Context, localSessionID string) ([]agentstore.Message, error) {
	local, err := h.localSession(ctx, localSessionID)
	if err != nil {
		return nil, err
	}
	return h.store.ListMessages(ctx, local.ID)
}

// SyncMessages refreshes one local transcript from OpenCode's message API.
// Only user and assistant text parts are projected; tool inputs, outputs,
// permissions, forms, and unknown message variants are intentionally omitted.
// The API does not expose a durable event cursor here, so each call performs a
// bounded transcript fetch and upserts the messages it recognizes.
func (h *openCodeSessionHistory) SyncMessages(ctx context.Context, client *opencode.Client, localSessionID string) ([]agentstore.Message, error) {
	local, err := h.localSession(ctx, localSessionID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("OpenCode server client is required")
	}

	projections, err := fetchOpenCodeTextMessages(ctx, client, local.OpenCodeSessionID)
	if err != nil {
		return nil, err
	}
	for _, projection := range projections {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		if err := h.store.AppendMessage(ctx, agentstore.Message{
			ID:                stableHistoryID("message", local.ID, projection.id),
			SessionID:         local.ID,
			OpenCodeMessageID: projection.id,
			Ordinal:           projection.ordinal,
			Role:              projection.role,
			Body:              projection.body,
			CreatedAt:         projection.createdAt,
		}); err != nil {
			return nil, err
		}
	}
	return h.store.ListMessages(ctx, local.ID)
}

func (h *openCodeSessionHistory) localSession(ctx context.Context, localSessionID string) (agentstore.Session, error) {
	if h == nil || h.store == nil {
		return agentstore.Session{}, errors.New("OpenCode session history is unavailable")
	}
	if err := contextError(ctx); err != nil {
		return agentstore.Session{}, err
	}
	if strings.TrimSpace(localSessionID) == "" {
		return agentstore.Session{}, errOpenCodeHistoryScope
	}
	sessions, err := h.ListSessions(ctx)
	if err != nil {
		return agentstore.Session{}, err
	}
	for _, session := range sessions {
		if session.ID == localSessionID {
			return session, nil
		}
	}
	return agentstore.Session{}, errOpenCodeHistoryScope
}

type openCodeMessageProjection struct {
	id        string
	sessionID string
	role      string
	body      string
	createdAt time.Time
	createdMS int64
	ordinal   int64
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

type openCodeMessageEnvelope struct {
	Info struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionID"`
		Role      string `json:"role"`
		Time      struct {
			Created float64 `json:"created"`
		} `json:"time"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
}

func fetchOpenCodeTextMessages(ctx context.Context, client *opencode.Client, sessionID string) ([]openCodeMessageProjection, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errOpenCodeHistoryScope
	}
	cursor := ""
	seenCursors := make(map[string]struct{})
	seenMessages := make(map[string]openCodeMessageProjection)
	totalBytes := 0
	for pageIndex := 0; pageIndex < openCodeHistoryMaxPages; pageIndex++ {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		page, err := client.ListMessages(ctx, sessionID, opencode.MessageListOptions{
			Limit:  openCodeHistoryPageSize,
			Order:  "asc",
			Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		for _, raw := range page.Data {
			totalBytes += len(raw)
			if totalBytes > openCodeHistoryMaxBytes {
				return nil, errors.New("OpenCode transcript exceeds the local synchronization limit")
			}
			projection, ok := projectOpenCodeTextMessage(raw, sessionID)
			if !ok {
				continue
			}
			seenMessages[projection.id] = projection
			if len(seenMessages) > openCodeHistoryMaxItems {
				return nil, errors.New("OpenCode transcript has too many messages to synchronize at once")
			}
		}
		if page.Cursor.Next == nil || strings.TrimSpace(*page.Cursor.Next) == "" {
			return orderOpenCodeMessages(seenMessages)
		}
		cursor = *page.Cursor.Next
		if _, duplicate := seenCursors[cursor]; duplicate {
			return nil, errors.New("OpenCode transcript pagination repeated a cursor")
		}
		seenCursors[cursor] = struct{}{}
	}
	return nil, errors.New("OpenCode transcript exceeds the local page limit")
}

func projectOpenCodeTextMessage(raw json.RawMessage, expectedSessionID string) (openCodeMessageProjection, bool) {
	if len(raw) == 0 || len(raw) > openCodeMessageMaxBytes {
		return openCodeMessageProjection{}, false
	}
	var envelope openCodeMessageEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return openCodeMessageProjection{}, false
	}
	if envelope.Info.ID == "" || len(envelope.Info.ID) > 256 || envelope.Info.SessionID == "" || expectedSessionID != "" && envelope.Info.SessionID != expectedSessionID {
		return openCodeMessageProjection{}, false
	}
	if envelope.Info.Role != "user" && envelope.Info.Role != "assistant" {
		return openCodeMessageProjection{}, false
	}
	createdMS, ok := openCodeTimestampMillis(envelope.Info.Time.Created)
	if !ok {
		return openCodeMessageProjection{}, false
	}
	var body strings.Builder
	for _, part := range envelope.Parts {
		if part.Type != "text" || part.Text == "" {
			continue
		}
		if body.Len() > 0 {
			body.WriteByte('\n')
		}
		body.WriteString(part.Text)
	}
	if body.Len() == 0 {
		return openCodeMessageProjection{}, false
	}
	return openCodeMessageProjection{
		id:        envelope.Info.ID,
		sessionID: envelope.Info.SessionID,
		role:      envelope.Info.Role,
		body:      body.String(),
		createdAt: time.UnixMilli(createdMS).UTC(),
		createdMS: createdMS,
		ordinal:   createdMS * openCodeOrdinalPerMillisecond,
	}, true
}

// projectOpenCodeMessages returns sorted text transcript entries from a page
// returned by the session message endpoint. It pins the entire projection to
// the first recognized message's session ID and discards any cross-session
// records in the same response.
func projectOpenCodeMessages(raw []json.RawMessage) []openCodeTranscriptMessage {
	var expectedSessionID string
	projected := make(map[string]openCodeMessageProjection)
	for _, item := range raw {
		message, ok := projectOpenCodeTextMessage(item, expectedSessionID)
		if !ok {
			continue
		}
		if expectedSessionID == "" {
			expectedSessionID = message.sessionID
		}
		projected[message.id] = message
	}
	ordered, err := orderOpenCodeMessages(projected)
	if err != nil {
		return nil
	}
	result := make([]openCodeTranscriptMessage, 0, len(ordered))
	for _, message := range ordered {
		result = append(result, openCodeTranscriptMessage{
			ID: message.id, Role: message.role, Text: message.body,
			Ordinal: message.ordinal, CreatedAt: message.createdAt,
		})
	}
	return result
}

func orderOpenCodeMessages(messages map[string]openCodeMessageProjection) ([]openCodeMessageProjection, error) {
	ordered := make([]openCodeMessageProjection, 0, len(messages))
	for _, message := range messages {
		ordered = append(ordered, message)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].createdMS != ordered[j].createdMS {
			return ordered[i].createdMS < ordered[j].createdMS
		}
		return ordered[i].id < ordered[j].id
	})
	for index := 0; index < len(ordered); {
		end := index + 1
		for end < len(ordered) && ordered[end].createdMS == ordered[index].createdMS {
			end++
		}
		if end-index > openCodeOrdinalPerMillisecond {
			return nil, errors.New("OpenCode transcript has too many messages with the same timestamp")
		}
		if ordered[index].createdMS > math.MaxInt64/openCodeOrdinalPerMillisecond {
			return nil, errors.New("OpenCode transcript timestamp is out of range")
		}
		for tieIndex := index; tieIndex < end; tieIndex++ {
			ordered[tieIndex].ordinal = ordered[tieIndex].createdMS*openCodeOrdinalPerMillisecond + int64(tieIndex-index)
		}
		index = end
	}
	return ordered, nil
}

func openCodeTimestampMillis(value float64) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > float64(math.MaxInt64/openCodeOrdinalPerMillisecond) {
		return 0, false
	}
	// OpenCode's JSON API uses JavaScript timestamps in milliseconds. Values
	// near the epoch are accepted as milliseconds for fixtures and old sessions.
	return int64(math.Round(value)), true
}

func openCodeSessionTime(value float64) time.Time {
	millis, ok := openCodeTimestampMillis(value)
	if !ok {
		return time.Time{}
	}
	return time.UnixMilli(millis).UTC()
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

func stableHistoryID(kind string, scope ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(kind))
	for _, value := range scope {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return kind + "-" + hex.EncodeToString(hash.Sum(nil)[:16])
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	return ctx.Err()
}
