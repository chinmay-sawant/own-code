// Package agenttools defines the tool registry, permission policy, and
// workspace-scoped executors used by the agent loop. It has no dependency on
// internal/app, internal/agent, or storage.
//
// Security model: an executor that needs approval verifies the approval token
// itself (see Approval.verify). Callers are not trusted. Dismissal, timeout,
// cancellation, a missing policy entry, and a missing approval are all denial.
package agenttools

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Decision is the static policy verdict for a tool.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionAsk   Decision = "ask"
	DecisionDeny  Decision = "deny"
)

// Policy maps tool names to decisions. A tool absent from the map is denied.
type Policy struct {
	rules map[string]Decision
}

// DefaultPolicy is the recorded allow/ask/deny matrix for the initial tools.
//
//	read_file    allow  (workspace-scoped, read-only)
//	list_files   allow  (workspace-scoped, read-only)
//	search_files allow  (workspace-scoped, read-only)
//	run_shell    ask    (explicit approval for every command)
//	write_file   ask    (stages a proposal in the proposal worktree only)
//	edit_file    ask    (stages a proposal in the proposal worktree only)
//	delete_file  ask    (stages a delete proposal; the checkout is untouched)
//	move_file    ask    (stages a rename proposal; the checkout is untouched)
func DefaultPolicy() *Policy {
	return &Policy{rules: map[string]Decision{
		ToolReadFile:    DecisionAllow,
		ToolListFiles:   DecisionAllow,
		ToolSearchFiles: DecisionAllow,
		ToolRunShell:    DecisionAsk,
		ToolWriteFile:   DecisionAsk,
		ToolEditFile:    DecisionAsk,
		ToolDeleteFile:  DecisionAsk,
		ToolMoveFile:    DecisionAsk,
	}}
}

// NewPolicy builds a policy from explicit rules.
func NewPolicy(rules map[string]Decision) *Policy {
	p := &Policy{rules: map[string]Decision{}}
	for k, v := range rules {
		p.rules[k] = v
	}
	return p
}

// Decide returns the decision for a tool. A nil policy, unknown tool, or
// unrecognized decision value is deny.
func (p *Policy) Decide(tool string) Decision {
	if p == nil {
		return DecisionDeny
	}
	switch d := p.rules[tool]; d {
	case DecisionAllow, DecisionAsk, DecisionDeny:
		return d
	}
	return DecisionDeny
}

// Outcome is how a user prompt ended.
type Outcome string

const (
	OutcomeGranted   Outcome = "granted"
	OutcomeDenied    Outcome = "denied"
	OutcomeDismissed Outcome = "dismissed"
	OutcomeTimeout   Outcome = "timeout"
	OutcomeCanceled  Outcome = "canceled"
)

// Binding identifies the one call an approval may authorize.
type Binding struct {
	SessionID    string
	TurnID       string
	CallID       string
	WorktreeRoot string
}

// NewApproval builds the approval for a prepared call. Only OutcomeGranted
// yields Granted=true; every other outcome (including unknown) is denial.
func NewApproval(b Binding, p Prepared, outcome Outcome) *Approval {
	return &Approval{
		SessionID:    b.SessionID,
		TurnID:       b.TurnID,
		CallID:       b.CallID,
		WorktreeRoot: b.WorktreeRoot,
		ArgsHash:     p.ArgsHash,
		Granted:      outcome == OutcomeGranted,
	}
}

// Authorize combines the policy with the prepared call's approval requirement.
// It returns the effective decision: deny wins, and a call that needs approval
// is at least ask even if the policy says allow.
func (p *Policy) Authorize(prep Prepared) Decision {
	d := p.Decide(prep.Name)
	if d == DecisionAllow && prep.NeedsApproval {
		return DecisionAsk
	}
	return d
}

// Approval is a user decision bound to one call.
type Approval struct {
	SessionID, TurnID, CallID, WorktreeRoot, ArgsHash string
	Granted                                           bool
}

// Errors returned for rejected approvals.
var (
	ErrApprovalRequired = errors.New("agenttools: approval required")
	ErrApprovalDenied   = errors.New("agenttools: approval denied")
	ErrApprovalStale    = errors.New("agenttools: approval does not match this call")
	ErrApprovalUsed     = errors.New("agenttools: approval already used")
	ErrNoBinding        = errors.New("agenttools: execution context has no call binding")
)

type bindingKey struct{}

// WithBinding records, in ctx, the session/turn/call/worktree the loop is
// dispatching. Executors that need approval compare the approval to this
// trusted binding, so an approval minted for another call is rejected.
func WithBinding(ctx context.Context, b Binding) context.Context {
	return context.WithValue(ctx, bindingKey{}, b)
}

func bindingFrom(ctx context.Context) (Binding, bool) {
	b, ok := ctx.Value(bindingKey{}).(Binding)
	return b, ok
}

// verify checks an approval against the expected args hash, worktree root and
// the context binding. Any problem is a rejection.
func (a *Approval) verify(ctx context.Context, argsHash, root string) error {
	if a == nil {
		return ErrApprovalRequired
	}
	if !a.Granted {
		return ErrApprovalDenied
	}
	b, ok := bindingFrom(ctx)
	if !ok || b.SessionID == "" || b.TurnID == "" || b.CallID == "" {
		return ErrNoBinding
	}
	if a.SessionID == "" || a.TurnID == "" || a.CallID == "" {
		return fmt.Errorf("%w: incomplete binding", ErrApprovalStale)
	}
	if a.SessionID != b.SessionID || a.TurnID != b.TurnID || a.CallID != b.CallID {
		return fmt.Errorf("%w: session, turn or call differs", ErrApprovalStale)
	}
	if a.WorktreeRoot == "" || a.WorktreeRoot != root || b.WorktreeRoot != root {
		return fmt.Errorf("%w: worktree differs", ErrApprovalStale)
	}
	if a.ArgsHash == "" || a.ArgsHash != argsHash {
		return fmt.Errorf("%w: arguments differ", ErrApprovalStale)
	}
	return nil
}

// onceGuard makes an approved execution single-use per Prepared.
type onceGuard struct {
	mu   sync.Mutex
	used bool
}

func (g *onceGuard) take() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.used {
		return false
	}
	g.used = true
	return true
}
