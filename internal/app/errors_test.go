package app

import (
	"errors"
	"testing"
)

func TestAppErrorWrapsAndUnwraps(t *testing.T) {
	cause := errors.New("handle limit")
	werr := NewWatchError(WatchOpAdd, "/root", cause)

	if !errors.Is(werr, cause) {
		t.Fatal("AppError should unwrap to its cause")
	}

	if !IsWatchError(werr) || !IsKind(werr, KindWatch) {
		t.Fatalf("expected KindWatch, got %+v", werr)
	}

	if IsKind(werr, KindGit) {
		t.Fatal("watch error must not report as git")
	}

	if got := AppErrorAt(werr); got != werr {
		t.Fatal("AppErrorAt should return the same envelope")
	}

	if AppErrorAt(cause) != nil {
		t.Fatal("plain error must not extract an AppError")
	}

	if NewWatchError(WatchOpAdd, "/root", nil) != nil {
		t.Fatal("nil cause must return nil")
	}
}

func TestAppErrorUserMessagesCoverAllWatchOps(t *testing.T) {
	ops := []WatchOp{
		WatchOpStartup,
		WatchOpWalk,
		WatchOpAdd,
		WatchOpGit,
		WatchOpEvents,
		WatchOpOverflow,
		WatchOpRescan,
		WatchOpChanges,
		WatchOpClosed,
	}

	for _, op := range ops {
		werr := NewWatchError(op, "/root", errors.New("boom"))
		if werr.UserMessage() == "" || werr.UserMessage() == "Something went wrong" {
			t.Errorf("op %q has no specific user message", op)
		}

		if werr.Error() == "" {
			t.Errorf("op %q has empty Error()", op)
		}
	}
}

func TestSetWatchErrorHandlesAllErrorShapes(t *testing.T) {
	app := newTestApp(t)

	// Plain errors become a startup AppError instead of a raw string note.
	app.setWatchError(errors.New("plain boom"))
	if !IsWatchError(app.watchErr) {
		t.Fatalf("plain error not wrapped: %+v", app.watchErr)
	}

	if app.watchNote == "" || app.watchNote == "watcher unavailable" {
		t.Fatalf("plain error note = %q, want UserMessage", app.watchNote)
	}

	// Typed errors keep their op so the UI can explain the degradation.
	typed := NewWatchError(WatchOpOverflow, "/root", errors.New("overflow"))
	app.setWatchError(typed)

	if app.watchErr != typed {
		t.Fatal("typed error should be kept as-is")
	}

	if app.watchNote != typed.UserMessage() {
		t.Fatalf("note = %q, want %q", app.watchNote, typed.UserMessage())
	}

	app.setWatchError(nil)

	if app.watchErr != nil {
		t.Fatal("nil should clear the recorded error")
	}
}
