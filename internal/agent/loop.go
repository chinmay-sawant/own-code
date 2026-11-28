package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
)

// Defaults for Config.
const (
	DefaultMaxSteps     = 12
	DefaultMaxArgsBytes = 64 * 1024
	partFlushBytes      = 512
	partFlushInterval   = 150 * time.Millisecond
	finalizeTimeout     = 5 * time.Second
)

var (
	// ErrSessionBusy is returned when a session already has a running turn.
	ErrSessionBusy = errors.New("agent: session already has a running turn")
	// ErrTurnExists is returned when the turn ID was used before. A turn is
	// never resumed or re-run; start a new turn instead.
	ErrTurnExists = errors.New("agent: turn already exists")
)

// ToolDefinition advertises one tool and whether it needs local approval.
type ToolDefinition struct {
	Name          string
	Description   string
	Schema        json.RawMessage
	NeedsApproval bool
}

// ToolOutput is what an executed tool returns.
type ToolOutput struct {
	Text      string
	Truncated bool
	ExitCode  *int
	Duration  time.Duration
}

// ToolApproval is a local authorization for exactly one call and argument
// hash. It never originates from the model.
type ToolApproval struct {
	SessionID, TurnID, CallID, WorktreeRoot, ArgsHash string
	Granted                                           bool
}

// PreparedCall is a validated, ready-to-run tool call.
type PreparedCall struct {
	Name          string
	Args          json.RawMessage
	ArgsHash      string
	NeedsApproval bool
	Summary       string
	Execute       func(ctx context.Context, appr ToolApproval) (ToolOutput, error)
}

// ToolRegistry validates and prepares calls. Prepare must reject unknown tools
// and invalid arguments without side effects.
type ToolRegistry interface {
	Specs() []ToolDefinition
	Prepare(name string, raw json.RawMessage) (PreparedCall, error)
}

// ApprovalRequest describes one pending call for the local approver.
type ApprovalRequest struct {
	SessionID, TurnID, CallID, WorktreeRoot string
	Name, Summary, ArgsHash                 string
}

// Approver asks the user (or a local policy). An error or a done context
// means deny. Decisions that do not match the request exactly are denied.
type Approver interface {
	Decide(ctx context.Context, req ApprovalRequest) (ToolApproval, error)
}

// Config configures a Runner.
type Config struct {
	Store        *agentstore.Store
	Provider     Provider
	Tools        ToolRegistry
	Approver     Approver
	System       string
	MaxSteps     int
	MaxArgsBytes int
	MaxOutput    int // provider MaxOutputTokens; 0 = provider default
	// BindContext, if set, wraps the context passed to Execute so the tool
	// layer can verify session, turn, call, and worktree IDs (the approval
	// carries them). Called immediately before Execute, after the dispatch claim.
	BindContext func(ctx context.Context, appr ToolApproval) context.Context
	Now         func() time.Time
}

// RunInput starts one turn.
type RunInput struct {
	SessionID    string
	TurnID       string
	UserText     string
	Model        ModelTarget
	WorktreeRoot string
	// Events, if set, receives provider events (called synchronously).
	Events func(Event)
}

// Outcome is the terminal state of a turn.
type Outcome struct {
	Status    agentstore.TurnStatus
	Reason    string // "final", "max_steps", "canceled", "provider_error", ...
	Text      string // final assistant text of the last step
	Steps     int
	ToolCalls int
}

// Runner runs turns. Each session has at most one active turn; different
// sessions share no mutable state and run concurrently.
type Runner struct {
	cfg    Config
	mu     sync.Mutex
	active map[string]*activeRun
}

type activeRun struct {
	turnID string
	cancel context.CancelFunc
}

// NewRunner validates cfg and applies defaults.
func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Store == nil || cfg.Provider == nil || cfg.Tools == nil {
		return nil, errors.New("agent: runner needs a store, provider, and tool registry")
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	if cfg.MaxArgsBytes <= 0 || cfg.MaxArgsBytes > DefaultMaxArgsBytes {
		cfg.MaxArgsBytes = DefaultMaxArgsBytes
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{cfg: cfg, active: map[string]*activeRun{}}, nil
}

// Cancel stops the active turn if it matches both IDs. A stale turn ID is a
// no-op, so a late Stop for an old turn cannot stop its successor.
func (r *Runner) Cancel(sessionID, turnID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.active[sessionID]
	if !ok || run.turnID != turnID {
		return false
	}
	run.cancel()
	return true
}

// Active reports the running turn ID for a session.
func (r *Runner) Active(sessionID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.active[sessionID]
	if !ok {
		return "", false
	}
	return run.turnID, true
}

// Run executes the bounded step loop for one turn and blocks until it ends.
// The turn is always left in a terminal store status when a store is
// reachable. A non-nil error means infrastructure failure; provider and tool
// failures are reported through Outcome.
func (r *Runner) Run(ctx context.Context, in RunInput) (Outcome, error) {
	if in.SessionID == "" || in.TurnID == "" {
		return Outcome{}, errors.New("agent: session and turn IDs are required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	if _, busy := r.active[in.SessionID]; busy {
		r.mu.Unlock()
		return Outcome{}, ErrSessionBusy
	}
	r.active[in.SessionID] = &activeRun{turnID: in.TurnID, cancel: cancel}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.active, in.SessionID)
		r.mu.Unlock()
	}()
	return r.run(runCtx, in)
}

type turnRun struct {
	r         *Runner
	in        RunInput
	overrides map[string]string
	out       Outcome
}

func (r *Runner) run(ctx context.Context, in RunInput) (Outcome, error) {
	t := &turnRun{r: r, in: in, overrides: map[string]string{}}
	store := r.cfg.Store
	turns, err := store.ListTurns(ctx, in.SessionID)
	if err != nil {
		return Outcome{}, err
	}
	for _, existing := range turns {
		if existing.ID == in.TurnID {
			return Outcome{}, ErrTurnExists
		}
	}
	userID := in.TurnID + "-user"
	if _, err := store.BeginTurn(ctx, agentstore.Turn{
		SessionID: in.SessionID, ID: in.TurnID, UserMessageID: userID,
		ProviderID: in.Model.ProviderID, ModelID: in.Model.ModelID, StartedAt: r.cfg.Now(),
	}); err != nil {
		return Outcome{}, err
	}
	if in.UserText != "" {
		if err := store.SaveTurnMessage(ctx, agentstore.TurnMessage{ID: userID, SessionID: in.SessionID, TurnID: in.TurnID, Role: "user", CreatedAt: r.cfg.Now()}); err != nil {
			return t.fail(ctx, "persist_error", err)
		}
		if err := store.SaveMessagePart(ctx, agentstore.MessagePart{SessionID: in.SessionID, MessageID: userID, ID: "text", Ordinal: 0, Kind: agentstore.PartText, Text: in.UserText, Status: agentstore.PartComplete, UpdatedAt: r.cfg.Now()}); err != nil {
			return t.fail(ctx, "persist_error", err)
		}
	}

	specs := r.cfg.Tools.Specs()
	known := map[string]ToolDefinition{}
	var providerTools []ToolSpec
	for _, s := range specs {
		known[s.Name] = s
		providerTools = append(providerTools, ToolSpec{Name: s.Name, Description: s.Description, Parameters: s.Schema})
	}
	if !in.Model.ToolsSupported {
		providerTools = nil
	}

	for step := 0; step < r.cfg.MaxSteps; step++ {
		if ctx.Err() != nil {
			return t.finish(agentstore.TurnInterrupted, "canceled", "canceled")
		}
		t.out.Steps = step + 1
		history, err := store.LoadHistory(ctx, in.SessionID)
		if err != nil {
			return t.fail(ctx, "persist_error", err)
		}
		messages := BuildMessages(history, t.overrides)
		if r.cfg.System != "" {
			messages = append([]Message{{Role: RoleSystem, Content: r.cfg.System}}, messages...)
		}
		assistantID := fmt.Sprintf("%s-a%d", in.TurnID, step)
		if err := store.SaveTurnMessage(ctx, agentstore.TurnMessage{ID: assistantID, SessionID: in.SessionID, TurnID: in.TurnID, Role: "assistant", CreatedAt: r.cfg.Now()}); err != nil {
			return t.fail(ctx, "persist_error", err)
		}
		sink := &partSink{store: store, session: in.SessionID, message: assistantID, now: r.cfg.Now}
		res, perr := r.cfg.Provider.Stream(ctx, Request{
			SessionID: in.SessionID, TurnID: in.TurnID, Model: in.Model,
			Messages: messages, Tools: providerTools, MaxOutputTokens: r.cfg.MaxOutput,
		}, func(e Event) {
			if e.Kind == EventTextDelta {
				sink.add(ctx, e.Text)
			}
			if in.Events != nil {
				in.Events(e)
			}
		})
		text := res.Text
		if text == "" {
			text = sink.text.String()
		}
		t.out.Text = text
		switch {
		case ctx.Err() != nil || res.Status == StatusInterrupted:
			sink.finish(ctx, text, agentstore.PartInterrupted)
			return t.finish(agentstore.TurnInterrupted, "canceled", "canceled")
		case perr != nil || res.Status == StatusFailed:
			sink.finish(ctx, text, agentstore.PartInterrupted)
			msg := "provider failed"
			if perr != nil {
				msg = perr.Error()
			}
			return t.finish(agentstore.TurnFailed, "provider_error", msg)
		}
		sink.finish(ctx, text, agentstore.PartComplete)
		if len(res.ToolCalls) == 0 {
			return t.finish(agentstore.TurnCompleted, "final", "")
		}
		if err := t.handleCalls(ctx, step, assistantID, res.ToolCalls, known); err != nil {
			return t.fail(ctx, "persist_error", err)
		}
		if ctx.Err() != nil {
			return t.finish(agentstore.TurnInterrupted, "canceled", "canceled")
		}
	}
	return t.finish(agentstore.TurnFailed, "max_steps", fmt.Sprintf("step limit of %d reached", r.cfg.MaxSteps))
}

// finish writes the terminal status with a context that survives cancellation.
func (t *turnRun) finish(status agentstore.TurnStatus, reason, msg string) (Outcome, error) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), finalizeTimeout)
	defer cancel()
	t.out.Status, t.out.Reason = status, reason
	err := t.r.cfg.Store.FinishTurn(wctx, t.in.SessionID, t.in.TurnID, status, msg, t.r.cfg.Now())
	return t.out, err
}

func (t *turnRun) fail(_ context.Context, reason string, cause error) (Outcome, error) {
	out, ferr := t.finish(agentstore.TurnFailed, reason, cause.Error())
	return out, errors.Join(cause, ferr)
}

// handleCalls persists, authorizes, and runs each call sequentially. Every
// requested call ends with a stored result so history stays protocol-complete.
func (t *turnRun) handleCalls(ctx context.Context, step int, messageID string, calls []ToolCall, known map[string]ToolDefinition) error {
	cfg := t.r.cfg
	wctx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
	}
	for i, call := range calls {
		t.out.ToolCalls++
		id := fmt.Sprintf("%s-s%d-c%d", t.in.TurnID, step, i)
		input := string(call.Arguments)
		oversize := len(call.Arguments) > cfg.MaxArgsBytes
		if oversize || !json.Valid(call.Arguments) {
			input = `{"_omitted":"arguments were oversized or not valid JSON and were not stored"}`
		}
		sw, cancel := wctx()
		_, err := cfg.Store.SaveToolCall(sw, agentstore.ToolCall{
			SessionID: t.in.SessionID, ID: id, MessageID: messageID, TurnID: t.in.TurnID,
			Ordinal: -1, ToolName: call.Name, Input: input, CreatedAt: cfg.Now(),
		})
		cancel()
		if err != nil {
			return err
		}
		t.emitTool(EventToolStart, id, call.Name, "")
		deny := func(decision agentstore.ApprovalDecision) error {
			sw, cancel := wctx()
			defer cancel()
			return cfg.Store.RecordToolApproval(sw, t.in.SessionID, id, decision, cfg.Now())
		}
		if ctx.Err() != nil {
			if err := deny(agentstore.ApprovalCanceled); err != nil {
				return err
			}
			t.emitTool(EventToolDone, id, call.Name, "skipped")
			continue
		}
		var reason string
		var prepared PreparedCall
		switch {
		case oversize:
			reason = fmt.Sprintf("Rejected: arguments exceed the %d byte limit; the tool was not run.", cfg.MaxArgsBytes)
		case !json.Valid(call.Arguments):
			reason = "Rejected: arguments are not complete valid JSON; the tool was not run."
		default:
			if _, ok := known[call.Name]; !ok {
				reason = fmt.Sprintf("Rejected: unknown tool %q; the tool was not run.", call.Name)
			} else if p, perr := cfg.Tools.Prepare(call.Name, call.Arguments); perr != nil {
				reason = fmt.Sprintf("Rejected: invalid arguments for %s: %v; the tool was not run.", call.Name, perr)
			} else if p.Execute == nil {
				reason = fmt.Sprintf("Rejected: tool %s is not executable; it was not run.", call.Name)
			} else {
				prepared = p
			}
		}
		if reason != "" {
			sw, cancel := wctx()
			err := cfg.Store.RejectToolCall(sw, t.in.SessionID, id, reason, cfg.Now())
			cancel()
			if err != nil {
				return err
			}
			t.emitTool(EventToolDone, id, call.Name, "rejected")
			continue
		}

		appr := ToolApproval{SessionID: t.in.SessionID, TurnID: t.in.TurnID, CallID: id, WorktreeRoot: t.in.WorktreeRoot, ArgsHash: prepared.ArgsHash, Granted: true}
		decision := agentstore.ApprovalApproved
		if prepared.NeedsApproval || known[call.Name].NeedsApproval {
			decision, appr = t.ask(ctx, id, prepared, appr)
		}
		if decision != agentstore.ApprovalApproved {
			if err := deny(decision); err != nil {
				return err
			}
			t.emitTool(EventToolDone, id, call.Name, "denied")
			continue
		}
		// Approval is persisted before dispatch.
		sw, cancel = wctx()
		err = cfg.Store.RecordToolApproval(sw, t.in.SessionID, id, agentstore.ApprovalApproved, cfg.Now())
		cancel()
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			// Approved but canceled before the claim: it never started.
			// The stored status stays approved; recovery marks it interrupted.
			t.overrides[id] = "Not run: the turn was canceled before dispatch."
			t.emitTool(EventToolDone, id, call.Name, "skipped")
			continue
		}
		sw, cancel = wctx()
		err = cfg.Store.BeginToolDispatch(sw, t.in.SessionID, id, cfg.Now())
		cancel()
		if errors.Is(err, agentstore.ErrAlreadyDispatched) {
			t.overrides[id] = "Not run: this call was already dispatched; it is never retried automatically."
			t.emitTool(EventToolDone, id, call.Name, "skipped")
			continue
		}
		if err != nil {
			return err
		}
		execCtx := ctx
		if cfg.BindContext != nil {
			execCtx = cfg.BindContext(ctx, appr)
		}
		out, xerr := execute(execCtx, prepared, appr)
		status := agentstore.ToolSucceeded
		text := formatOutput(out)
		if xerr != nil {
			status = agentstore.ToolFailed
			text = strings.TrimSpace("error: " + xerr.Error() + "\n" + text)
		} else if ctx.Err() != nil {
			status = agentstore.ToolFailed
			text = strings.TrimSpace("canceled\n" + text)
		}
		sw, cancel = wctx()
		err = cfg.Store.CompleteToolCall(sw, t.in.SessionID, id, status, text, cfg.Now())
		cancel()
		if err != nil {
			return err
		}
		t.emitTool(EventToolDone, id, call.Name, string(status))
	}
	return nil
}

// emitTool reports tool lifecycle to the optional Events consumer.
func (t *turnRun) emitTool(kind EventKind, id, name, status string) {
	if t.in.Events == nil {
		return
	}
	t.in.Events(Event{Kind: kind, SessionID: t.in.SessionID, TurnID: t.in.TurnID, Text: status, ToolCall: &ToolCall{ID: id, Name: name}})
}

// ask obtains a decision. Anything but an exact, granted match is a denial.
func (t *turnRun) ask(ctx context.Context, id string, p PreparedCall, want ToolApproval) (agentstore.ApprovalDecision, ToolApproval) {
	ap := t.r.cfg.Approver
	if ap == nil {
		return agentstore.ApprovalDenied, ToolApproval{}
	}
	got, err := ap.Decide(ctx, ApprovalRequest{
		SessionID: t.in.SessionID, TurnID: t.in.TurnID, CallID: id, WorktreeRoot: t.in.WorktreeRoot,
		Name: p.Name, Summary: p.Summary, ArgsHash: p.ArgsHash,
	})
	switch {
	case ctx.Err() != nil:
		return agentstore.ApprovalCanceled, ToolApproval{}
	case err != nil:
		return agentstore.ApprovalDenied, ToolApproval{}
	case !got.Granted:
		return agentstore.ApprovalDenied, ToolApproval{}
	case got.SessionID != want.SessionID || got.TurnID != want.TurnID || got.CallID != want.CallID ||
		got.ArgsHash != want.ArgsHash || got.WorktreeRoot != want.WorktreeRoot:
		return agentstore.ApprovalDenied, ToolApproval{} // stale or foreign approval
	}
	return agentstore.ApprovalApproved, got
}

func execute(ctx context.Context, p PreparedCall, appr ToolApproval) (out ToolOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool panicked: %v", r)
		}
	}()
	return p.Execute(ctx, appr)
}

func formatOutput(o ToolOutput) string {
	var b strings.Builder
	b.WriteString(o.Text)
	if o.Truncated {
		b.WriteString("\n[output truncated]")
	}
	if o.ExitCode != nil {
		fmt.Fprintf(&b, "\n[exit code %d]", *o.ExitCode)
	}
	return strings.TrimSpace(b.String())
}

// partSink persists streamed text as one streaming part, throttled.
type partSink struct {
	store     *agentstore.Store
	session   string
	message   string
	now       func() time.Time
	text      strings.Builder
	flushed   int
	lastFlush time.Time
}

func (p *partSink) add(ctx context.Context, delta string) {
	if delta == "" {
		return
	}
	p.text.WriteString(delta)
	if p.text.Len()-p.flushed >= partFlushBytes || time.Since(p.lastFlush) >= partFlushInterval {
		p.write(ctx, p.text.String(), agentstore.PartStreaming)
	}
}

func (p *partSink) write(ctx context.Context, text string, status agentstore.PartStatus) {
	_ = p.store.SaveMessagePart(ctx, agentstore.MessagePart{
		SessionID: p.session, MessageID: p.message, ID: "text", Ordinal: 0,
		Kind: agentstore.PartText, Text: text, Status: status, UpdatedAt: p.now(),
	})
	p.flushed, p.lastFlush = len(text), time.Now()
}

func (p *partSink) finish(ctx context.Context, text string, status agentstore.PartStatus) {
	if text == "" {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
	defer cancel()
	p.write(wctx, text, status)
}
