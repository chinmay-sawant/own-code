package agentstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Size limits for Console turn data. Tool input is never truncated because a
// truncated argument set must not be validated or executed; oversize input is
// rejected instead. Display text and tool output are truncated with a marker.
const (
	maxPartText      = 256 * 1024
	maxToolInput     = 64 * 1024
	maxToolResult    = 64 * 1024
	maxTurnError     = 4 * 1024
	truncationMarker = "\n[truncated]"
)

var (
	ErrTooLarge          = errors.New("agent store: value exceeds its size limit")
	ErrConflict          = errors.New("agent store: conflicting update to a finished record")
	ErrAlreadyDispatched = errors.New("agent store: tool call is not awaiting dispatch")
)

type TurnStatus string

const (
	TurnRunning     TurnStatus = "running"
	TurnCompleted   TurnStatus = "completed"
	TurnFailed      TurnStatus = "failed"
	TurnInterrupted TurnStatus = "interrupted"
)

type Turn struct {
	SessionID     string
	ID            string
	Ordinal       int64
	UserMessageID string
	ProviderID    string
	ModelID       string
	Status        TurnStatus
	Error         string
	StartedAt     time.Time
	EndedAt       time.Time // zero while running
}

type PartKind string

const (
	PartText     PartKind = "text"
	PartToolCall PartKind = "tool_call"
)

type PartStatus string

const (
	PartStreaming   PartStatus = "streaming"
	PartComplete    PartStatus = "complete"
	PartInterrupted PartStatus = "interrupted"
)

type MessagePart struct {
	SessionID  string
	MessageID  string
	ID         string
	Ordinal    int64 // negative appends after the existing parts
	Kind       PartKind
	Text       string
	ToolCallID string
	Status     PartStatus
	UpdatedAt  time.Time
}

type ToolCallStatus string

const (
	ToolRequested   ToolCallStatus = "requested"
	ToolApproved    ToolCallStatus = "approved"
	ToolDenied      ToolCallStatus = "denied"
	ToolRunning     ToolCallStatus = "running" // durable dispatch claim
	ToolSucceeded   ToolCallStatus = "succeeded"
	ToolFailed      ToolCallStatus = "failed"
	ToolInterrupted ToolCallStatus = "interrupted" // never started
	ToolUnknown     ToolCallStatus = "unknown"     // may have started; never retry
)

// ApprovalDecision is the local authorization outcome. Anything other than
// ApprovalApproved denies the call.
type ApprovalDecision string

const (
	ApprovalApproved  ApprovalDecision = "approved"
	ApprovalDenied    ApprovalDecision = "denied"
	ApprovalDismissed ApprovalDecision = "dismissed"
	ApprovalTimeout   ApprovalDecision = "timeout"
	ApprovalCanceled  ApprovalDecision = "canceled"
)

type ToolCall struct {
	SessionID         string
	ID                string
	MessageID         string
	TurnID            string
	Ordinal           int64 // negative appends
	PartID            string
	ToolName          string
	Input             string // complete JSON arguments
	Status            ToolCallStatus
	Approval          ApprovalDecision
	ApprovalAt        time.Time
	DispatchStartedAt time.Time
	Result            string
	ResultTruncated   bool
	IsError           bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Terminal reports whether no further transition is allowed.
func (s ToolCallStatus) Terminal() bool {
	switch s {
	case ToolDenied, ToolSucceeded, ToolFailed, ToolInterrupted, ToolUnknown:
		return true
	}
	return false
}

// TurnMessage identifies a message that belongs to a Console turn.
type TurnMessage struct {
	ID        string
	SessionID string
	TurnID    string
	Role      string
	CreatedAt time.Time
}

type HistoryPart struct {
	Kind     PartKind
	Text     string
	Status   PartStatus
	ToolCall *ToolCall
}

// HistoryMessage is one message rebuilt from the database. Legacy messages
// without parts appear with a single text part holding the stored body.
type HistoryMessage struct {
	ID        string
	TurnID    string
	Role      string
	CreatedAt time.Time
	Parts     []HistoryPart
}

type RecoveryReport struct {
	TurnsInterrupted int
	CallsInterrupted int
	CallsUnknown     int
	PartsInterrupted int
}

const (
	unknownOutcomeText   = "Outcome unknown: the application stopped while this tool call was running. It was not retried; inspect the workspace before trusting or repeating it."
	notRunInterruptText  = "Not run: the application stopped before this tool call was dispatched."
	deniedResultTemplate = "Denied: the tool call was not approved (%s) and was not run."
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization|proxy-authorization|x-api-key|api[-_]?key)(["']?\s*[:=]\s*["']?)(bearer\s+)?[^\s"',;]+`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=\-]{8,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`),
}

// redactSecrets removes obvious credentials from text that is about to be
// persisted. The store has no field for API keys; this is defense in depth.
func redactSecrets(value string) string {
	value = secretPatterns[0].ReplaceAllString(value, "${1}${2}[redacted]")
	value = secretPatterns[1].ReplaceAllString(value, "Bearer [redacted]")
	return secretPatterns[2].ReplaceAllString(value, "[redacted]")
}

func boundedMarked(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	return bounded(value, maxBytes-len(truncationMarker)) + truncationMarker, true
}

func nextOrdinal(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	var next int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&next); err != nil {
		return 0, err
	}
	return next, nil
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.UnixMilli()
}

func nullMillis(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

// BeginTurn records a new running turn. Repeating the call for the same turn
// ID returns the stored row unchanged.
func (s *Store) BeginTurn(ctx context.Context, turn Turn) (Turn, error) {
	if err := requiredID("session ID", turn.SessionID); err != nil {
		return Turn{}, err
	}
	if err := requiredID("turn ID", turn.ID); err != nil {
		return Turn{}, err
	}
	if len(turn.UserMessageID) > maxIDBytes {
		return Turn{}, errors.New("agent store: user message ID must be at most 256 bytes")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Turn{}, fmt.Errorf("agent store: begin turn: %w", err)
	}
	defer tx.Rollback()
	ordinal, err := nextOrdinal(ctx, tx, `SELECT COALESCE(MAX(ordinal) + 1, 0) FROM agent_turns WHERE session_id = ?`, turn.SessionID)
	if err != nil {
		return Turn{}, fmt.Errorf("agent store: allocate turn ordinal: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_turns (session_id, id, ordinal, user_message_id, provider_id, model_id, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?, 'running', ?)
		ON CONFLICT(session_id, id) DO NOTHING`,
		turn.SessionID, turn.ID, ordinal, turn.UserMessageID,
		bounded(turn.ProviderID, maxModelBytes), bounded(turn.ModelID, maxModelBytes), millis(turn.StartedAt)); err != nil {
		return Turn{}, fmt.Errorf("agent store: begin turn: %w", err)
	}
	stored, err := scanTurn(tx.QueryRowContext(ctx, turnSelect+` WHERE session_id = ? AND id = ?`, turn.SessionID, turn.ID))
	if err != nil {
		return Turn{}, fmt.Errorf("agent store: read turn: %w", err)
	}
	if stored.Status == TurnRunning {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET status = 'running', updated_at = MAX(updated_at, ?) WHERE id = ?`,
			millis(turn.StartedAt), turn.SessionID); err != nil {
			return Turn{}, fmt.Errorf("agent store: mark session running: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Turn{}, fmt.Errorf("agent store: commit turn: %w", err)
	}
	return stored, nil
}

// FinishTurn sets a terminal status. Repeating the same status is a no-op; a
// different status for an already finished turn returns ErrConflict.
func (s *Store) FinishTurn(ctx context.Context, sessionID, turnID string, status TurnStatus, message string, at time.Time) error {
	switch status {
	case TurnCompleted, TurnFailed, TurnInterrupted:
	default:
		return fmt.Errorf("agent store: %q is not a terminal turn status", status)
	}
	message, _ = boundedMarked(redactSecrets(message), maxTurnError)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agent store: begin finish turn: %w", err)
	}
	defer tx.Rollback()
	var current TurnStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM agent_turns WHERE session_id = ? AND id = ?`, sessionID, turnID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("agent store: read turn status: %w", err)
	}
	if current != TurnRunning {
		if current == status {
			return nil
		}
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_turns SET status = ?, error = ?, ended_at = ? WHERE session_id = ? AND id = ?`,
		string(status), message, millis(at), sessionID, turnID); err != nil {
		return fmt.Errorf("agent store: finish turn: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET status = ?, updated_at = MAX(updated_at, ?) WHERE id = ?`,
		string(status), millis(at), sessionID); err != nil {
		return fmt.Errorf("agent store: update session status: %w", err)
	}
	return tx.Commit()
}

func (s *Store) ListTurns(ctx context.Context, sessionID string) ([]Turn, error) {
	rows, err := s.db.QueryContext(ctx, turnSelect+` WHERE session_id = ? ORDER BY ordinal`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent store: list turns: %w", err)
	}
	defer rows.Close()
	var turns []Turn
	for rows.Next() {
		turn, err := scanTurn(rows)
		if err != nil {
			return nil, fmt.Errorf("agent store: scan turn: %w", err)
		}
		turns = append(turns, turn)
	}
	return turns, rows.Err()
}

const turnSelect = `SELECT session_id, id, ordinal, user_message_id, provider_id, model_id, status, error, started_at, ended_at FROM agent_turns`

func scanTurn(row rowScanner) (Turn, error) {
	var turn Turn
	var started int64
	var ended sql.NullInt64
	if err := row.Scan(&turn.SessionID, &turn.ID, &turn.Ordinal, &turn.UserMessageID, &turn.ProviderID, &turn.ModelID,
		&turn.Status, &turn.Error, &started, &ended); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Turn{}, ErrNotFound
		}
		return Turn{}, err
	}
	turn.StartedAt = time.UnixMilli(started).UTC()
	if ended.Valid {
		turn.EndedAt = time.UnixMilli(ended.Int64).UTC()
	}
	return turn, nil
}

// SaveTurnMessage creates a message in display order, or re-links an existing
// one to its turn. Existing text, ordinal, and parts are preserved.
func (s *Store) SaveTurnMessage(ctx context.Context, message TurnMessage) error {
	for name, value := range map[string]string{"message ID": message.ID, "session ID": message.SessionID} {
		if err := requiredID(name, value); err != nil {
			return err
		}
	}
	if len(message.TurnID) > maxIDBytes {
		return errors.New("agent store: turn ID must be at most 256 bytes")
	}
	if strings.TrimSpace(message.Role) == "" || len(message.Role) > 32 {
		return errors.New("agent store: message role is required and must be at most 32 bytes")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agent store: begin message: %w", err)
	}
	defer tx.Rollback()
	ordinal, err := nextOrdinal(ctx, tx, `SELECT COALESCE(MAX(ordinal) + 1, 0) FROM agent_messages WHERE session_id = ?`, message.SessionID)
	if err != nil {
		return fmt.Errorf("agent store: allocate message ordinal: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_messages (id, session_id, opencode_message_id, ordinal, role, body, created_at, turn_id)
		VALUES (?, ?, ?, ?, ?, '', ?, ?)
		ON CONFLICT(session_id, opencode_message_id) DO UPDATE SET turn_id = excluded.turn_id`,
		message.ID, message.SessionID, message.ID, ordinal, message.Role, millis(message.CreatedAt), message.TurnID); err != nil {
		return fmt.Errorf("agent store: save message: %w", err)
	}
	return tx.Commit()
}

// SaveMessagePart upserts one part. A finished (complete or interrupted) part
// is never reopened by a late streaming write. The message's legacy body is
// kept equal to its text parts so older readers still see the transcript.
func (s *Store) SaveMessagePart(ctx context.Context, part MessagePart) error {
	for name, value := range map[string]string{"session ID": part.SessionID, "message ID": part.MessageID, "part ID": part.ID} {
		if err := requiredID(name, value); err != nil {
			return err
		}
	}
	switch part.Kind {
	case PartText:
	case PartToolCall:
		return errors.New("agent store: tool-call parts are created by SaveToolCall")
	default:
		return fmt.Errorf("agent store: invalid part kind %q", part.Kind)
	}
	if part.Status == "" {
		part.Status = PartComplete
	}
	switch part.Status {
	case PartStreaming, PartComplete, PartInterrupted:
	default:
		return fmt.Errorf("agent store: invalid part status %q", part.Status)
	}
	text, _ := boundedMarked(part.Text, maxPartText)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agent store: begin part: %w", err)
	}
	defer tx.Rollback()
	if err := requireMessage(ctx, tx, part.SessionID, part.MessageID); err != nil {
		return err
	}
	var existing PartStatus
	err = tx.QueryRowContext(ctx, `SELECT status FROM message_parts WHERE session_id = ? AND message_id = ? AND id = ?`,
		part.SessionID, part.MessageID, part.ID).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		ordinal := part.Ordinal
		if ordinal < 0 {
			ordinal, err = nextOrdinal(ctx, tx, `SELECT COALESCE(MAX(ordinal) + 1, 0) FROM message_parts WHERE session_id = ? AND message_id = ?`, part.SessionID, part.MessageID)
			if err != nil {
				return fmt.Errorf("agent store: allocate part ordinal: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_parts (session_id, message_id, id, ordinal, kind, text, tool_call_id, status, updated_at)
			VALUES (?, ?, ?, ?, 'text', ?, '', ?, ?)`,
			part.SessionID, part.MessageID, part.ID, ordinal, text, string(part.Status), millis(part.UpdatedAt)); err != nil {
			return fmt.Errorf("agent store: save part: %w", err)
		}
	case err != nil:
		return fmt.Errorf("agent store: read part: %w", err)
	case existing != PartStreaming:
		return nil // finished parts are immutable; retries are no-ops
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE message_parts SET text = ?, status = ?, updated_at = ?
			WHERE session_id = ? AND message_id = ? AND id = ?`,
			text, string(part.Status), millis(part.UpdatedAt), part.SessionID, part.MessageID, part.ID); err != nil {
			return fmt.Errorf("agent store: update part: %w", err)
		}
	}
	if err := syncBody(ctx, tx, part.SessionID, part.MessageID); err != nil {
		return err
	}
	return tx.Commit()
}

func requireMessage(ctx context.Context, tx *sql.Tx, sessionID, messageID string) error {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM agent_messages WHERE session_id = ? AND id = ?`, sessionID, messageID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("agent store: check message: %w", err)
	}
	return nil
}

func syncBody(ctx context.Context, tx *sql.Tx, sessionID, messageID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT text FROM message_parts WHERE session_id = ? AND message_id = ? AND kind = 'text' ORDER BY ordinal, id`, sessionID, messageID)
	if err != nil {
		return fmt.Errorf("agent store: read message text: %w", err)
	}
	var body strings.Builder
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			rows.Close()
			return fmt.Errorf("agent store: scan message text: %w", err)
		}
		body.WriteString(text)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE agent_messages SET body = ? WHERE session_id = ? AND id = ?`,
		bounded(body.String(), maxMessageBytes), sessionID, messageID); err != nil {
		return fmt.Errorf("agent store: update message body: %w", err)
	}
	return nil
}

// SaveToolCall records a complete tool-call request. Input must be complete
// valid JSON within the size limit; it is rejected, never truncated. A repeat
// for the same call ID returns the stored row unchanged, so retries cannot
// reset approval or dispatch state.
func (s *Store) SaveToolCall(ctx context.Context, call ToolCall) (ToolCall, error) {
	for name, value := range map[string]string{"session ID": call.SessionID, "message ID": call.MessageID, "tool call ID": call.ID} {
		if err := requiredID(name, value); err != nil {
			return ToolCall{}, err
		}
	}
	if len(call.TurnID) > maxIDBytes || len(call.PartID) > maxIDBytes {
		return ToolCall{}, errors.New("agent store: turn and part IDs must be at most 256 bytes")
	}
	if strings.TrimSpace(call.ToolName) == "" || len(call.ToolName) > maxToolNameBytes {
		return ToolCall{}, errors.New("agent store: tool name is required and must be at most 128 bytes")
	}
	if len(call.Input) > maxToolInput {
		return ToolCall{}, fmt.Errorf("%w: tool input exceeds %d bytes", ErrTooLarge, maxToolInput)
	}
	if !json.Valid([]byte(call.Input)) {
		return ToolCall{}, errors.New("agent store: tool input must be complete valid JSON")
	}
	input := redactSecrets(call.Input)
	if !json.Valid([]byte(input)) {
		input = call.Input
	}
	partID := call.PartID
	if partID == "" {
		partID = "call-" + call.ID
		if len(partID) > maxIDBytes {
			return ToolCall{}, errors.New("agent store: tool call ID is too long to derive a part ID")
		}
	}
	now := millis(call.CreatedAt)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ToolCall{}, fmt.Errorf("agent store: begin tool call: %w", err)
	}
	defer tx.Rollback()
	if err := requireMessage(ctx, tx, call.SessionID, call.MessageID); err != nil {
		return ToolCall{}, err
	}
	ordinal := call.Ordinal
	if ordinal < 0 {
		if ordinal, err = nextOrdinal(ctx, tx, `SELECT COALESCE(MAX(ordinal) + 1, 0) FROM agent_tool_calls WHERE session_id = ?`, call.SessionID); err != nil {
			return ToolCall{}, fmt.Errorf("agent store: allocate tool call ordinal: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO agent_tool_calls (session_id, id, message_id, turn_id, ordinal, tool_name, input, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'requested', ?, ?)
		ON CONFLICT(session_id, id) DO NOTHING`,
		call.SessionID, call.ID, call.MessageID, call.TurnID, ordinal, call.ToolName, input, now, now)
	if err != nil {
		return ToolCall{}, fmt.Errorf("agent store: save tool call: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		partOrdinal, err := nextOrdinal(ctx, tx, `SELECT COALESCE(MAX(ordinal) + 1, 0) FROM message_parts WHERE session_id = ? AND message_id = ?`, call.SessionID, call.MessageID)
		if err != nil {
			return ToolCall{}, fmt.Errorf("agent store: allocate part ordinal: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_parts (session_id, message_id, id, ordinal, kind, text, tool_call_id, status, updated_at)
			VALUES (?, ?, ?, ?, 'tool_call', '', ?, 'complete', ?)
			ON CONFLICT(session_id, message_id, id) DO NOTHING`,
			call.SessionID, call.MessageID, partID, partOrdinal, call.ID, now); err != nil {
			return ToolCall{}, fmt.Errorf("agent store: save tool call part: %w", err)
		}
	}
	stored, err := scanToolCall(tx.QueryRowContext(ctx, toolCallSelect+` WHERE session_id = ? AND id = ?`, call.SessionID, call.ID))
	if err != nil {
		return ToolCall{}, fmt.Errorf("agent store: read tool call: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ToolCall{}, fmt.Errorf("agent store: commit tool call: %w", err)
	}
	return stored, nil
}

// RecordToolApproval persists the local decision before dispatch. Only
// ApprovalApproved allows dispatch; every other decision denies the call and
// stores a result so history stays protocol-complete. Repeating the same
// decision is a no-op; a different decision returns ErrConflict.
func (s *Store) RecordToolApproval(ctx context.Context, sessionID, callID string, decision ApprovalDecision, at time.Time) error {
	switch decision {
	case ApprovalApproved, ApprovalDenied, ApprovalDismissed, ApprovalTimeout, ApprovalCanceled:
	default:
		decision = ApprovalDenied // unknown decisions fail closed
	}
	return s.updateToolCall(ctx, sessionID, callID, func(call ToolCall) (string, []any, error) {
		if call.Status != ToolRequested {
			if call.Approval == decision {
				return "", nil, nil
			}
			return "", nil, ErrConflict
		}
		if decision == ApprovalApproved {
			return `status = 'approved', approval = ?, approval_at = ?, updated_at = ?`, []any{string(decision), millis(at), millis(at)}, nil
		}
		return `status = 'denied', approval = ?, approval_at = ?, updated_at = ?, result = ?, is_error = 1`,
			[]any{string(decision), millis(at), millis(at), fmt.Sprintf(deniedResultTemplate, decision)}, nil
	})
}

// RejectToolCall records that a requested call failed validation (unknown
// tool, invalid or oversized arguments) before any approval or dispatch. The
// call moves requested -> failed with a durable, redacted, bounded message so
// the model and the transcript see why. Repeating it on an already failed call
// is a no-op; any other state returns ErrConflict.
func (s *Store) RejectToolCall(ctx context.Context, sessionID, callID, message string, at time.Time) error {
	message, _ = boundedMarked(redactSecrets(message), maxToolResult)
	return s.updateToolCall(ctx, sessionID, callID, func(call ToolCall) (string, []any, error) {
		if call.Status == ToolFailed && call.DispatchStartedAt.IsZero() {
			return "", nil, nil
		}
		if call.Status != ToolRequested {
			return "", nil, ErrConflict
		}
		return `status = 'failed', result = ?, is_error = 1, updated_at = ?`, []any{message, millis(at)}, nil
	})
}

// ListCallsNeedingInspection returns the session's calls whose outcome is
// unknown (dispatch may have begun) or that were interrupted. The user must
// inspect the side effects; a retry is never automatic and must be a new call
// ID with fresh approval (a stored call can never be dispatched again).
func (s *Store) ListCallsNeedingInspection(ctx context.Context, sessionID string) ([]ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, toolCallSelect+` WHERE session_id = ? AND status IN ('unknown', 'interrupted') ORDER BY ordinal, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent store: list calls needing inspection: %w", err)
	}
	defer rows.Close()
	var out []ToolCall
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, call)
	}
	return out, rows.Err()
}

// BeginToolDispatch atomically claims an approved call (approved -> running).
// Any other state, including an earlier claim, returns ErrAlreadyDispatched so
// a retry can never execute the call twice.
func (s *Store) BeginToolDispatch(ctx context.Context, sessionID, callID string, at time.Time) error {
	return s.updateToolCall(ctx, sessionID, callID, func(call ToolCall) (string, []any, error) {
		if call.Status != ToolApproved {
			return "", nil, ErrAlreadyDispatched
		}
		return `status = 'running', dispatch_started_at = ?, updated_at = ?`, []any{millis(at), millis(at)}, nil
	})
}

// CompleteToolCall stores the result of a running call. Output is redacted
// and truncated with a marker. Repeating the same terminal status is a no-op.
func (s *Store) CompleteToolCall(ctx context.Context, sessionID, callID string, status ToolCallStatus, output string, at time.Time) error {
	if status != ToolSucceeded && status != ToolFailed {
		return fmt.Errorf("agent store: %q is not a completion status", status)
	}
	output, truncated := boundedMarked(redactSecrets(output), maxToolResult)
	return s.updateToolCall(ctx, sessionID, callID, func(call ToolCall) (string, []any, error) {
		if call.Status == status {
			return "", nil, nil
		}
		if call.Status != ToolRunning {
			return "", nil, ErrConflict
		}
		return `status = ?, result = ?, result_truncated = ?, is_error = ?, updated_at = ?`,
			[]any{string(status), output, boolInt(truncated), boolInt(status == ToolFailed), millis(at)}, nil
	})
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// updateToolCall runs a read-check-write cycle in one transaction. decide
// returns the SET clause and its args, or an empty clause for a no-op.
func (s *Store) updateToolCall(ctx context.Context, sessionID, callID string, decide func(ToolCall) (string, []any, error)) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agent store: begin tool call update: %w", err)
	}
	defer tx.Rollback()
	call, err := scanToolCall(tx.QueryRowContext(ctx, toolCallSelect+` WHERE session_id = ? AND id = ?`, sessionID, callID))
	if err != nil {
		return err
	}
	set, args, err := decide(call)
	if err != nil {
		return err
	}
	if set == "" {
		return nil
	}
	args = append(args, sessionID, callID)
	if _, err := tx.ExecContext(ctx, `UPDATE agent_tool_calls SET `+set+` WHERE session_id = ? AND id = ?`, args...); err != nil {
		return fmt.Errorf("agent store: update tool call: %w", err)
	}
	return tx.Commit()
}

func (s *Store) GetToolCall(ctx context.Context, sessionID, callID string) (ToolCall, error) {
	return scanToolCall(s.db.QueryRowContext(ctx, toolCallSelect+` WHERE session_id = ? AND id = ?`, sessionID, callID))
}

const toolCallSelect = `SELECT session_id, id, message_id, turn_id, ordinal, tool_name, input, status, approval, approval_at,
	dispatch_started_at, result, result_truncated, is_error, created_at, updated_at FROM agent_tool_calls`

func scanToolCall(row rowScanner) (ToolCall, error) {
	var call ToolCall
	var approvalAt, dispatchAt sql.NullInt64
	var truncated, isError int
	var created, updated int64
	if err := row.Scan(&call.SessionID, &call.ID, &call.MessageID, &call.TurnID, &call.Ordinal, &call.ToolName, &call.Input,
		&call.Status, &call.Approval, &approvalAt, &dispatchAt, &call.Result, &truncated, &isError, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ToolCall{}, ErrNotFound
		}
		return ToolCall{}, err
	}
	if approvalAt.Valid {
		call.ApprovalAt = time.UnixMilli(approvalAt.Int64).UTC()
	}
	if dispatchAt.Valid {
		call.DispatchStartedAt = time.UnixMilli(dispatchAt.Int64).UTC()
	}
	call.ResultTruncated = truncated != 0
	call.IsError = isError != 0
	call.CreatedAt = time.UnixMilli(created).UTC()
	call.UpdatedAt = time.UnixMilli(updated).UTC()
	return call, nil
}

// LoadHistory rebuilds a session transcript, in display order, from the local
// database alone. Tool calls carry their stored result; calls that never ran
// carry an explanatory result so every request has a matching response.
func (s *Store) LoadHistory(ctx context.Context, sessionID string) ([]HistoryMessage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("agent store: begin history read: %w", err)
	}
	defer tx.Rollback()

	callRows, err := tx.QueryContext(ctx, toolCallSelect+` WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent store: read tool calls: %w", err)
	}
	calls := map[string]*ToolCall{}
	for callRows.Next() {
		call, err := scanToolCall(callRows)
		if err != nil {
			callRows.Close()
			return nil, fmt.Errorf("agent store: scan tool call: %w", err)
		}
		if call.Result == "" && call.Status.Terminal() {
			call.Result = notRunInterruptText
		}
		calls[call.ID] = &call
	}
	if err := callRows.Err(); err != nil {
		callRows.Close()
		return nil, err
	}
	callRows.Close()

	messageRows, err := tx.QueryContext(ctx, `SELECT id, turn_id, role, body, created_at FROM agent_messages WHERE session_id = ? ORDER BY ordinal, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent store: read messages: %w", err)
	}
	var history []HistoryMessage
	byID := map[string]int{}
	bodies := map[string]string{}
	for messageRows.Next() {
		var message HistoryMessage
		var body string
		var created int64
		if err := messageRows.Scan(&message.ID, &message.TurnID, &message.Role, &body, &created); err != nil {
			messageRows.Close()
			return nil, fmt.Errorf("agent store: scan message: %w", err)
		}
		message.CreatedAt = time.UnixMilli(created).UTC()
		byID[message.ID] = len(history)
		bodies[message.ID] = body
		history = append(history, message)
	}
	if err := messageRows.Err(); err != nil {
		messageRows.Close()
		return nil, err
	}
	messageRows.Close()

	partRows, err := tx.QueryContext(ctx, `SELECT message_id, kind, text, tool_call_id, status FROM message_parts WHERE session_id = ? ORDER BY message_id, ordinal, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent store: read parts: %w", err)
	}
	for partRows.Next() {
		var messageID, toolCallID string
		var part HistoryPart
		if err := partRows.Scan(&messageID, &part.Kind, &part.Text, &toolCallID, &part.Status); err != nil {
			partRows.Close()
			return nil, fmt.Errorf("agent store: scan part: %w", err)
		}
		index, ok := byID[messageID]
		if !ok {
			continue
		}
		if part.Kind == PartToolCall {
			call, ok := calls[toolCallID]
			if !ok {
				continue
			}
			copied := *call
			part.ToolCall = &copied
		}
		history[index].Parts = append(history[index].Parts, part)
	}
	if err := partRows.Err(); err != nil {
		partRows.Close()
		return nil, err
	}
	partRows.Close()

	for i := range history {
		if len(history[i].Parts) == 0 && bodies[history[i].ID] != "" {
			history[i].Parts = []HistoryPart{{Kind: PartText, Text: bodies[history[i].ID], Status: PartComplete}}
		}
	}
	return history, nil
}

// RecoverInterrupted finalizes work left unfinished by a previous process.
// The caller must hold profile ownership first, otherwise it could mark
// another live process's turns. It never re-dispatches anything:
//   - running turns and streaming parts become interrupted;
//   - calls that were running (dispatch may have begun) become unknown;
//   - requested or approved calls that never started become interrupted.
//
// Sessions that were queued or running become interrupted.
func (s *Store) RecoverInterrupted(ctx context.Context, at time.Time) (RecoveryReport, error) {
	now := millis(at)
	var report RecoveryReport
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("agent store: begin recovery: %w", err)
	}
	defer tx.Rollback()
	count := func(query string, args ...any) (int, error) {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, fmt.Errorf("agent store: recovery: %w", err)
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}
	// Sessions first, while the unfinished rows are still identifiable.
	if _, err = count(`UPDATE agent_sessions SET status = 'interrupted', updated_at = MAX(updated_at, ?)
		WHERE status IN ('queued', 'running') AND (
			id IN (SELECT session_id FROM agent_turns WHERE status = 'running')
			OR id IN (SELECT session_id FROM agent_tool_calls WHERE status IN ('requested', 'approved', 'running')))`, now); err != nil {
		return report, err
	}
	if report.CallsUnknown, err = count(`UPDATE agent_tool_calls SET status = 'unknown', result = CASE WHEN result = '' THEN ? ELSE result END, is_error = 1, updated_at = ? WHERE status = 'running'`, unknownOutcomeText, now); err != nil {
		return report, err
	}
	if report.CallsInterrupted, err = count(`UPDATE agent_tool_calls SET status = 'interrupted', result = ?, is_error = 1, updated_at = ? WHERE status IN ('requested', 'approved')`, notRunInterruptText, now); err != nil {
		return report, err
	}
	if report.PartsInterrupted, err = count(`UPDATE message_parts SET status = 'interrupted', updated_at = ? WHERE status = 'streaming'`, now); err != nil {
		return report, err
	}
	if report.TurnsInterrupted, err = count(`UPDATE agent_turns SET status = 'interrupted', ended_at = ? WHERE status = 'running'`, now); err != nil {
		return report, err
	}
	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("agent store: commit recovery: %w", err)
	}
	return report, nil
}
