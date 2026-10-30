package app

import (
	"errors"
	"fmt"
)

// ErrorKind names the subsystem that produced an AppError. Keep the set small
// and stable: callers switch on Kind to decide whether the app can keep
// running with a degraded note or must surface a toast.
type ErrorKind string

const (
	KindWorkspace ErrorKind = "workspace"
	KindWatch     ErrorKind = "watch"
	KindGit       ErrorKind = "git"
	KindSession   ErrorKind = "session"
	KindIO        ErrorKind = "io"
)

// WatchOp narrows a KindWatch error to the operation that failed so logs and
// the status line can explain what degraded.
type WatchOp string

const (
	WatchOpStartup  WatchOp = "startup"
	WatchOpWalk     WatchOp = "walk"
	WatchOpAdd      WatchOp = "add"
	WatchOpGit      WatchOp = "git-watches"
	WatchOpEvents   WatchOp = "events"
	WatchOpOverflow WatchOp = "overflow"
	WatchOpRescan   WatchOp = "rescan"
	WatchOpChanges  WatchOp = "changes"
	WatchOpClosed   WatchOp = "closed"
)

// AppError is the single global error envelope for the app. It carries a
// stable Kind/Op for programmatic handling, the filesystem Path involved,
// and the underlying cause for logs. Use NewAppError at the site that has
// context and handle it with AppErrorAt/UserMessage at the boundary.
type AppError struct {
	Kind ErrorKind
	Op   WatchOp
	Path string
	Err  error
}

// NewAppError wraps err with kind/op/path context. A nil err returns nil so
// call sites can defer-wrap without branching.
func NewAppError(kind ErrorKind, op WatchOp, path string, err error) *AppError {
	if err == nil {
		return nil
	}

	// Avoid double-wrapping an AppError of the same kind/op: keep the
	// outermost path, preserve the root cause.
	var inner *AppError
	if errors.As(err, &inner) && inner.Kind == kind && inner.Op == op {
		if path != "" {
			inner.Path = path
		}

		return inner
	}

	return &AppError{Kind: kind, Op: op, Path: path, Err: err}
}

// NewWatchError is shorthand for the watcher subsystem.
func NewWatchError(op WatchOp, path string, err error) *AppError {
	return NewAppError(KindWatch, op, path, err)
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}

	where := string(e.Op)
	if e.Path != "" {
		where += " " + e.Path
	}

	if e.Err != nil {
		return fmt.Sprintf("%s %s: %v", e.Kind, where, e.Err)
	}

	return fmt.Sprintf("%s %s", e.Kind, where)
}

// Unwrap exposes the cause for errors.Is/As.
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

// UserMessage is the short, non-technical line shown in the status bar or a
// toast. Technical detail stays in Error()/logs.
func (e *AppError) UserMessage() string {
	if e == nil {
		return ""
	}

	switch e.Kind {
	case KindWatch:
		switch e.Op {
		case WatchOpStartup:
			return "File watcher unavailable: live updates off"
		case WatchOpWalk:
			return "File watcher limited: could not scan folders"
		case WatchOpAdd:
			return "File watcher limited: some folders are not watched"
		case WatchOpOverflow:
			return "File watcher overflow: rescanning workspace"
		case WatchOpRescan, WatchOpChanges:
			return "File watcher update failed: showing last state"
		case WatchOpClosed:
			return "File watcher closed"
		default:
			return "File watcher unavailable: live updates off"
		}
	case KindWorkspace:
		return "Workspace unavailable"
	case KindGit:
		return "Git update failed: showing last state"
	case KindSession:
		return "Session save failed"
	default:
		return "Something went wrong"
	}
}

// AppErrorAt extracts the *AppError from err, if any.
func AppErrorAt(err error) *AppError {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr
	}

	return nil
}

// IsKind reports whether err carries the given global kind.
func IsKind(err error, kind ErrorKind) bool {
	return AppErrorAt(err) != nil && AppErrorAt(err).Kind == kind
}

// IsWatchError reports whether err came from the watcher subsystem.
func IsWatchError(err error) bool { return IsKind(err, KindWatch) }
