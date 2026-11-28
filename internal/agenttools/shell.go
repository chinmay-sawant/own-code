package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	maxCommandBytes   = 8 * 1024
	maxStreamBytes    = 32 * 1024
	defaultShellLimit = 60
	maxShellLimit     = 300
)

type shellArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// boundedBuffer keeps the first limit bytes and counts the rest.
type boundedBuffer struct {
	limit   int
	buf     []byte
	dropped int64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.buf); room > 0 {
		if len(p) <= room {
			b.buf = append(b.buf, p...)
			return len(p), nil
		}
		b.buf = append(b.buf, p[:room]...)
		b.dropped += int64(len(p) - room)
		return len(p), nil
	}
	b.dropped += int64(len(p))
	return len(p), nil
}

func (b *boundedBuffer) text() string {
	s := strings.ToValidUTF8(string(b.buf), "�")
	if b.dropped > 0 {
		s += fmt.Sprintf("\n[output truncated: %d bytes omitted]", b.dropped)
	}
	return s
}

func runShellTool() tool {
	return tool{
		spec: Spec{
			Name:          ToolRunShell,
			Description:   "Run a shell command in the workspace root. Requires user approval for every command.",
			Schema:        schema(`{"type":"object","properties":{"command":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":300}},"required":["command"],"additionalProperties":false}`),
			NeedsApproval: true,
		},
		prepare: func(r *registry, raw json.RawMessage) (any, string, func(context.Context) (Output, error), error) {
			var a shellArgs
			if err := decodeStrict(raw, &a); err != nil {
				return nil, "", nil, err
			}
			if strings.TrimSpace(a.Command) == "" || len(a.Command) > maxCommandBytes || strings.ContainsRune(a.Command, 0) {
				return nil, "", nil, fmt.Errorf("%w: command required (max %d bytes)", ErrInvalidArgs, maxCommandBytes)
			}
			if a.TimeoutSeconds == 0 {
				a.TimeoutSeconds = defaultShellLimit
			}
			if a.TimeoutSeconds < 0 || a.TimeoutSeconds > maxShellLimit {
				return nil, "", nil, fmt.Errorf("%w: timeout_seconds must be 1..%d", ErrInvalidArgs, maxShellLimit)
			}
			summary := fmt.Sprintf("Run shell command:\n%s\n\nWorking directory: %s\nTime limit: %ds\nThis runs with your user's filesystem and network access; the working directory does not restrict it.",
				a.Command, r.root, a.TimeoutSeconds)
			run := func(ctx context.Context) (Output, error) {
				return runShell(ctx, r.root, a)
			}
			return a, summary, run, nil
		},
	}
}

func runShell(ctx context.Context, dir string, a shellArgs) (Output, error) {
	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, time.Duration(a.TimeoutSeconds)*time.Second)
	defer cancel()

	cmd := shellCommand(tctx, a.Command)
	cmd.Dir = dir
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	cmd.WaitDelay = 2 * time.Second
	stdout := &boundedBuffer{limit: maxStreamBytes}
	stderr := &boundedBuffer{limit: maxStreamBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	err := cmd.Run()
	out := Output{Duration: time.Since(start), Truncated: stdout.dropped > 0 || stderr.dropped > 0}
	var sb strings.Builder
	sb.WriteString("stdout:\n" + stdout.text() + "\nstderr:\n" + stderr.text() + "\n")
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		sb.WriteString("[canceled]\n")
		out.Text = sb.String()
		return out, ctx.Err()
	case errors.Is(tctx.Err(), context.DeadlineExceeded):
		fmt.Fprintf(&sb, "[timed out after %ds; process tree killed]\n", a.TimeoutSeconds)
		out.Text = sb.String()
		code := -1
		out.ExitCode = &code
		return out, nil
	case errors.As(err, &exitErr):
		code := exitErr.ExitCode()
		out.ExitCode = &code
	case err != nil:
		out.Text = sb.String()
		return out, err
	default:
		code := 0
		out.ExitCode = &code
	}
	fmt.Fprintf(&sb, "exit status: %d\n", *out.ExitCode)
	out.Text = sb.String()
	return out, nil
}
