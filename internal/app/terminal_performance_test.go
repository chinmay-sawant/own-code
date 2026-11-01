package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Exercise the page input and tick APIs, then lay out the resulting frame.
// The marker is assembled by printf so terminal echo cannot satisfy the test.
func TestTerminalRenderedLatency(t *testing.T) {
	a := newTerminalTestApp(t)
	defer a.Close()
	ctx := context.Background()
	if err := a.Page().Redraw(ctx); err != nil {
		t.Fatal(err)
	}
	clickID(t, a, "act-terminal")
	times := make([]time.Duration, 0, 20)
	for i := range 20 {
		start := time.Now()
		if err := a.Page().Edit().Type(ctx, fmt.Sprintf("printf 'latency-%%s\\n' %d", i)); err != nil {
			t.Fatal(err)
		}
		if err := a.Page().Edit().KeyDown(ctx, "enter"); err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("latency-%d", i)
		deadline := time.Now().Add(terminalTestTimeout)
		for !strings.Contains(terminalCopyText(a.View().Terminal), want) {
			if err := a.Page().Testing().Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("missing %s", want)
			}
			time.Sleep(time.Millisecond)
		}
		if err := a.Page().Redraw(ctx); err != nil {
			t.Fatal(err)
		}
		times = append(times, time.Since(start))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("input through PTY, model update and layout: p50=%s p95=%s max=%s", times[10], times[18], times[19])
	if path := os.Getenv("OWN_CODE_TERMINAL_SHOT"); path != "" {
		if err := os.WriteFile(path, a.Page().Probe().PNG(), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTerminalSustainedOutput(t *testing.T) {
	a := newTerminalTestApp(t)
	defer a.Close()
	ctx := context.Background()
	toggleTerminalForTest(t, a, ctx)
	start := time.Now()
	sendTerminalCommand(t, a, ctx, "seq 1 100000; printf 'flood-%s\\n' done")
	deadline := time.Now().Add(terminalTestTimeout)
	for !strings.Contains(terminalCopyText(a.View().Terminal), "flood-done") {
		if err := a.Page().Testing().Tick(ctx); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("output did not drain within 5s")
		}
		time.Sleep(time.Millisecond)
	}
	elapsed := time.Since(start)
	t.Logf("100000 output lines parsed and reflected in model in %s", elapsed)
	sendTerminalCommand(t, a, ctx, "printf 'after-%s\\n' flood")
	waitTerminalContains(t, a, "after-flood")
	clickTerminal(t, a, ctx, "term-scroll-up", "term-scroll-up")
	if a.View().Terminal.Scroll == 0 {
		t.Fatal("cannot scroll into history after flood")
	}
}
