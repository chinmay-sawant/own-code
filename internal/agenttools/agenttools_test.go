package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func newReg(t *testing.T) (*registry, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := newRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, r.root
}

func bind(root string) Binding {
	return Binding{SessionID: "s1", TurnID: "t1", CallID: "c1", WorktreeRoot: root}
}

func prepShell(t *testing.T, r *registry, cmd string) Prepared {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"command": cmd})
	p, err := r.Prepare(ToolRunShell, raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPolicyMatrix(t *testing.T) {
	p := DefaultPolicy()
	want := map[string]Decision{ToolReadFile: DecisionAllow, ToolListFiles: DecisionAllow, ToolSearchFiles: DecisionAllow,
		ToolRunShell: DecisionAsk, "write_file": DecisionDeny, "edit_file": DecisionDeny, "mystery": DecisionDeny}
	for k, v := range want {
		if got := p.Decide(k); got != v {
			t.Errorf("%s: got %s want %s", k, got, v)
		}
	}
	var nilp *Policy
	if nilp.Decide(ToolReadFile) != DecisionDeny {
		t.Error("nil policy must deny")
	}
	if NewPolicy(map[string]Decision{"x": "bogus"}).Decide("x") != DecisionDeny {
		t.Error("bogus decision must deny")
	}
	if NewPolicy(map[string]Decision{ToolRunShell: DecisionAllow}).Authorize(Prepared{Name: ToolRunShell, NeedsApproval: true}) != DecisionAsk {
		t.Error("needs-approval tool must not be plain allow")
	}
}

func TestShellNeverRunsWithoutValidApproval(t *testing.T) {
	r, root := newReg(t)
	marker := filepath.Join(root, "ran")
	p := prepShell(t, r, "touch ran")
	if !p.NeedsApproval {
		t.Fatal("shell must need approval")
	}
	good := NewApproval(bind(root), p, OutcomeGranted)

	other := prepShell(t, r, "touch other")
	cases := map[string]struct {
		ctx context.Context
		ap  *Approval
	}{
		"nil":           {WithBinding(context.Background(), bind(root)), nil},
		"denied":        {WithBinding(context.Background(), bind(root)), NewApproval(bind(root), p, OutcomeDenied)},
		"dismissed":     {WithBinding(context.Background(), bind(root)), NewApproval(bind(root), p, OutcomeDismissed)},
		"timeout":       {WithBinding(context.Background(), bind(root)), NewApproval(bind(root), p, OutcomeTimeout)},
		"canceled":      {WithBinding(context.Background(), bind(root)), NewApproval(bind(root), p, OutcomeCanceled)},
		"unknown":       {WithBinding(context.Background(), bind(root)), NewApproval(bind(root), p, Outcome("???"))},
		"no binding":    {context.Background(), good},
		"stale args":    {WithBinding(context.Background(), bind(root)), NewApproval(bind(root), other, OutcomeGranted)},
		"other session": {WithBinding(context.Background(), Binding{"s2", "t1", "c1", root}), good},
		"other turn":    {WithBinding(context.Background(), Binding{"s1", "t2", "c1", root}), good},
		"other call":    {WithBinding(context.Background(), Binding{"s1", "t1", "c2", root}), good},
		"other root": {WithBinding(context.Background(), bind(root)), func() *Approval {
			a := *good
			a.WorktreeRoot = root + "x"
			return &a
		}()},
		"empty ids":        {WithBinding(context.Background(), Binding{"", "", "", root}), &Approval{WorktreeRoot: root, ArgsHash: p.ArgsHash, Granted: true}},
		"ungranted-forged": {WithBinding(context.Background(), bind(root)), &Approval{"s1", "t1", "c1", root, p.ArgsHash, false}},
	}
	for name, c := range cases {
		if _, err := p.Execute(c.ctx, c.ap); err == nil {
			t.Errorf("%s: executed without valid approval", name)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s: executor ran", name)
		}
	}
	// Canceled context never reaches executor even with a good approval.
	cctx, cancel := context.WithCancel(WithBinding(context.Background(), bind(root)))
	cancel()
	if _, err := p.Execute(cctx, good); err == nil {
		t.Error("canceled ctx executed")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("executor ran on canceled ctx")
	}

	// Valid approval runs, exactly once.
	ctx := WithBinding(context.Background(), bind(root))
	out, err := p.Execute(ctx, good)
	if err != nil || out.ExitCode == nil || *out.ExitCode != 0 {
		t.Fatalf("valid run: %v %+v", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("marker missing")
	}
	if _, err := p.Execute(ctx, good); !errors.Is(err, ErrApprovalUsed) {
		t.Errorf("reuse: %v", err)
	}
}

func TestShellOutputExitAndTruncation(t *testing.T) {
	r, root := newReg(t)
	p := prepShell(t, r, "echo hi; echo err >&2; head -c 100000 /dev/zero | tr '\\0' a; exit 3")
	out, err := p.Execute(WithBinding(context.Background(), bind(root)), NewApproval(bind(root), p, OutcomeGranted))
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitCode == nil || *out.ExitCode != 3 || !out.Truncated || !strings.Contains(out.Text, "[output truncated:") || !strings.Contains(out.Text, "err") {
		t.Fatalf("%+v", out)
	}
	if out.Duration <= 0 || len(out.Text) > 2*maxStreamBytes+200 {
		t.Fatalf("duration/size: %v %d", out.Duration, len(out.Text))
	}
	if !strings.Contains(p.Summary, "ls") && !strings.Contains(p.Summary, root) || !strings.Contains(p.Summary, "filesystem and network access") {
		t.Fatalf("summary: %s", p.Summary)
	}
}

func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	f := strings.Fields(string(b))
	return len(f) > 2 && f[2] != "Z"
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestShellCancelKillsProcessTree(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("needs /proc")
	}
	r, root := newReg(t)
	p := prepShell(t, r, "sleep 120 & echo $! > child.pid; wait")
	ctx, cancel := context.WithCancel(WithBinding(context.Background(), bind(root)))
	done := make(chan error, 1)
	go func() { _, err := p.Execute(ctx, NewApproval(bind(root), p, OutcomeGranted)); done <- err }()
	var pid int
	deadline := timeAfter(5)
	for pid == 0 {
		if b, err := os.ReadFile(filepath.Join(root, "child.pid")); err == nil {
			for _, c := range strings.TrimSpace(string(b)) {
				if c >= '0' && c <= '9' {
					pid = pid*10 + int(c-'0')
				}
			}
		}
		select {
		case <-deadline:
			t.Fatal("child pid never written")
		default:
		}
		sleepMs(20)
	}
	if !alive(pid) {
		t.Fatal("child not running before cancel")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err: %v", err)
		}
	case <-timeAfter(10):
		t.Fatal("execute did not return after cancel")
	}
	for i := 0; i < 100 && alive(pid); i++ {
		sleepMs(20)
	}
	if alive(pid) {
		t.Fatal("grandchild survived cancel")
	}
}

func TestShellTimeout(t *testing.T) {
	r, root := newReg(t)
	raw := json.RawMessage(`{"command":"sleep 30","timeout_seconds":1}`)
	p, err := r.Prepare(ToolRunShell, raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Execute(WithBinding(context.Background(), bind(root)), NewApproval(bind(root), p, OutcomeGranted))
	if err != nil || !strings.Contains(out.Text, "timed out") || out.Duration > 10*1e9 {
		t.Fatalf("%v %+v", err, out)
	}
}

func TestInvalidInputRejectedBeforeExecutor(t *testing.T) {
	r, _ := newReg(t)
	big := `{"command":"` + strings.Repeat("a", MaxArgsBytes) + `"}`
	bad := []struct {
		tool, args string
	}{
		{"nope", `{}`},
		{"write_file", `{"path":"a","content":"b"}`},
		{ToolRunShell, `{"command":""}`},
		{ToolRunShell, `{"command":"ls","extra":1}`},
		{ToolRunShell, `{"command":"ls"} {}`},
		{ToolRunShell, `{"command":"ls","timeout_seconds":9999}`},
		{ToolRunShell, `not json`},
		{ToolRunShell, big},
		{ToolRunShell, `{"command":"` + strings.Repeat("a", maxCommandBytes+1) + `"}`},
		{ToolReadFile, `{}`},
		{ToolReadFile, `{"path":"/etc/passwd"}`},
		{ToolReadFile, `{"path":"../x"}`},
		{ToolReadFile, `{"path":"a/../../x"}`},
		{ToolReadFile, `{"path":"a","offset":-1}`},
		{ToolListFiles, `{"path":"/"}`},
		{ToolSearchFiles, `{"pattern":"x","path":".."}`},
		{ToolSearchFiles, `{"pattern":""}`},
	}
	for _, c := range bad {
		p, err := r.Prepare(c.tool, json.RawMessage(c.args))
		if err == nil || p.Execute != nil {
			t.Errorf("%s %.40s: accepted", c.tool, c.args)
		}
	}
}

func TestFilesystemTools(t *testing.T) {
	r, root := newReg(t)
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("hello\nneedle here\n"), 0o644)
	os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat("x", maxReadBytes+10)), 0o644)
	ctx := context.Background()
	run := func(tool, args string) (Output, error) {
		p, err := r.Prepare(tool, json.RawMessage(args))
		if err != nil {
			return Output{}, err
		}
		if p.NeedsApproval {
			t.Fatal("read tools must not need approval")
		}
		return p.Execute(ctx, nil)
	}
	if o, err := run(ToolReadFile, `{"path":"sub/a.txt"}`); err != nil || !strings.Contains(o.Text, "needle") {
		t.Fatal(o, err)
	}
	if o, err := run(ToolReadFile, `{"path":"big.txt"}`); err != nil || !o.Truncated || !strings.Contains(o.Text, "[truncated") {
		t.Fatal(err)
	}
	if o, err := run(ToolListFiles, `{}`); err != nil || !strings.Contains(o.Text, "sub/") {
		t.Fatal(o, err)
	}
	if o, err := run(ToolSearchFiles, `{"pattern":"needle"}`); err != nil || !strings.Contains(o.Text, "sub/a.txt:2:") {
		t.Fatal(o, err)
	}
}

func TestSymlinkEscapeRejectedAtExecution(t *testing.T) {
	r, root := newReg(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("top secret"), 0o644)
	// Link created AFTER Prepare passes lexical checks: recheck at execution.
	p, err := r.Prepare(ToolReadFile, json.RawMessage(`{"path":"link/secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := p.Execute(context.Background(), nil); !errors.Is(err, ErrOutside) {
		t.Fatalf("expected ErrOutside, got %v", err)
	}
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "flink"))
	p, _ = r.Prepare(ToolReadFile, json.RawMessage(`{"path":"flink"}`))
	if _, err := p.Execute(context.Background(), nil); !errors.Is(err, ErrOutside) {
		t.Fatalf("file link: %v", err)
	}
	p, _ = r.Prepare(ToolListFiles, json.RawMessage(`{"path":"link"}`))
	if _, err := p.Execute(context.Background(), nil); !errors.Is(err, ErrOutside) {
		t.Fatalf("list link: %v", err)
	}
	// Search must not follow links out of the tree.
	p, _ = r.Prepare(ToolSearchFiles, json.RawMessage(`{"pattern":"secret"}`))
	if o, err := p.Execute(context.Background(), nil); err != nil || strings.Contains(o.Text, "top secret") || strings.Contains(o.Text, "link") {
		t.Fatalf("search followed link: %v %q", err, o.Text)
	}
}

func TestCanonicalHashStable(t *testing.T) {
	r, _ := newReg(t)
	a, _ := r.Prepare(ToolRunShell, json.RawMessage(`{ "command":"ls" }`))
	b, _ := r.Prepare(ToolRunShell, json.RawMessage(`{"timeout_seconds":60,"command":"ls"}`))
	c, _ := r.Prepare(ToolRunShell, json.RawMessage(`{"command":"ls -a"}`))
	if a.ArgsHash != b.ArgsHash || a.ArgsHash == c.ArgsHash {
		t.Fatal("hash not canonical/sensitive")
	}
	if len(r.Specs()) != 4 {
		t.Fatal("specs")
	}
}
