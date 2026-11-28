// Package agentbridge adapts internal/agenttools to the interfaces defined by
// internal/agent so the two packages stay independent of each other. It adds
// no policy of its own: approvals are still verified by agenttools executors.
package agentbridge

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/chinmay-sawant/own-code/internal/agent"
	"github.com/chinmay-sawant/own-code/internal/agenttools"
)

// NewRegistry returns the default agenttools registry for worktreeRoot
// (read_file, list_files, search_files, run_shell) as an agent.ToolRegistry.
func NewRegistry(worktreeRoot string) (agent.ToolRegistry, error) {
	return NewRegistryWith(worktreeRoot)
}

// NewRegistryWith is NewRegistry with agenttools options, for example
// agenttools.WithProposalRoot and agenttools.WithProposalSink to enable the
// proposal-only write_file and edit_file tools.
func NewRegistryWith(worktreeRoot string, opts ...agenttools.Option) (agent.ToolRegistry, error) {
	reg, err := agenttools.NewRegistry(worktreeRoot, opts...)
	if err != nil {
		return nil, err
	}
	return registry{reg}, nil
}

type registry struct{ inner agenttools.Registry }

func (r registry) Specs() []agent.ToolDefinition {
	specs := r.inner.Specs()
	out := make([]agent.ToolDefinition, len(specs))
	for i, s := range specs {
		out[i] = agent.ToolDefinition{Name: s.Name, Description: s.Description, Schema: s.Schema, NeedsApproval: s.NeedsApproval}
	}
	return out
}

func (r registry) Prepare(name string, raw json.RawMessage) (agent.PreparedCall, error) {
	p, err := r.inner.Prepare(name, raw)
	if err != nil {
		return agent.PreparedCall{}, err
	}
	call := agent.PreparedCall{
		Name: p.Name, Args: p.Args, ArgsHash: p.ArgsHash, NeedsApproval: p.NeedsApproval, Summary: p.Summary,
	}
	if p.Execute != nil {
		call.Execute = func(ctx context.Context, a agent.ToolApproval) (agent.ToolOutput, error) {
			out, err := p.Execute(ctx, &agenttools.Approval{
				SessionID: a.SessionID, TurnID: a.TurnID, CallID: a.CallID,
				WorktreeRoot: a.WorktreeRoot, ArgsHash: a.ArgsHash, Granted: a.Granted,
			})
			return agent.ToolOutput{Text: out.Text, Truncated: out.Truncated, ExitCode: out.ExitCode, Duration: out.Duration}, err
		}
	}
	return call, nil
}

// BindContext records the dispatched call's identity in ctx so agenttools
// executors can verify the approval against it. Use it as agent.Config.BindContext.
func BindContext(ctx context.Context, a agent.ToolApproval) context.Context {
	return agenttools.WithBinding(ctx, agenttools.Binding{
		SessionID: a.SessionID, TurnID: a.TurnID, CallID: a.CallID, WorktreeRoot: a.WorktreeRoot,
	})
}

// NewPolicyApprover returns an agent.Approver that consults policy before
// ever calling ask. A tool the policy denies (including a nil policy or an
// unlisted tool) is refused without asking. Otherwise ask is called, because
// the loop only requests approval for calls that need it, and a policy "allow"
// is raised to "ask" for those. A nil ask, an ask error, or a done context is a
// denial. A grant is bound to exactly the request's session, turn, call,
// worktree and arguments hash.
func NewPolicyApprover(policy *agenttools.Policy, ask func(ctx context.Context, req agent.ApprovalRequest) (granted bool, err error)) agent.Approver {
	return approver{policy: policy, ask: ask}
}

type approver struct {
	policy *agenttools.Policy
	ask    func(ctx context.Context, req agent.ApprovalRequest) (bool, error)
}

var errDenied = errors.New("agentbridge: approval denied")

func (a approver) Decide(ctx context.Context, req agent.ApprovalRequest) (agent.ToolApproval, error) {
	if err := ctx.Err(); err != nil {
		return agent.ToolApproval{}, err
	}
	if a.policy.Authorize(agenttools.Prepared{Name: req.Name, NeedsApproval: true}) != agenttools.DecisionAsk || a.ask == nil {
		return agent.ToolApproval{}, errDenied
	}
	granted, err := a.ask(ctx, req)
	if err != nil {
		return agent.ToolApproval{}, err
	}
	if err := ctx.Err(); err != nil {
		return agent.ToolApproval{}, err
	}
	return agent.ToolApproval{
		SessionID: req.SessionID, TurnID: req.TurnID, CallID: req.CallID, WorktreeRoot: req.WorktreeRoot,
		ArgsHash: req.ArgsHash, Granted: granted,
	}, nil
}
