package app

import (
	"context"
	"errors"
	"time"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// Generic async LSP requests. The server goroutine runs a small closure
// (lspOpCall) that starts the request on its own goroutine; the reply comes
// back as an lspEvFunc that runs on the app loop. This keeps each feature
// (completion, references, rename, symbols) in its own file and out of the
// shared sync code.

const lspReqTimeout = 8 * time.Second

var errLSPNotSynced = errors.New("document is not synced with the server")

// lspDocRequest is the result handler of a position/document request. fresh
// is false when the document changed after the request was sent; callers
// that cannot use a stale answer return early.
type lspDocRequest func(ctx context.Context, res any, err error, fresh bool) bool

// flushDoc sends a pending debounced edit so the server (a FIFO queue) sees
// the same text as the editor before a request. It reports false when the
// document cannot be synced.
func (m *lspManager) flushDoc(ld *lspDoc) bool {
	if ld.pendingAt.IsZero() {
		return true
	}

	ld.pendingAt = time.Time{}

	text, fromDisk := m.docText(ld.path)
	if !fromDisk && len(text) > maxFileBytes {
		return false
	}

	ld.version++
	ld.server.enqueue(lspOp{
		kind: lspOpChange, uri: ld.uri, abs: ld.abs,
		version: ld.version, text: text, fromDisk: fromDisk,
	})

	return true
}

// syncedDoc returns the synced document for path after flushing edits.
func (a *App) syncedDoc(path string) (*lspManager, *lspDoc) {
	m := a.lspm
	if m == nil || !m.enabled {
		return nil, nil
	}

	ld := m.docs[path]
	if ld == nil || ld.skipped || ld.server == nil || !m.flushDoc(ld) {
		return nil, nil
	}

	return m, ld
}

// callDoc sends work for the document at path with the position (line, rune
// col) converted against the exact text the server holds. It reports false
// when no server can answer. done runs on the app loop.
func (m *lspManager) callDoc(ld *lspDoc, line, col int,
	work func(ctx context.Context, c *lsp.Client, uri string, pos lsp.Position) (any, error), done lspDocRequest,
) {
	srv, path, version, uri := ld.server, ld.path, ld.version, ld.uri

	m.callServer(srv, uri, func(ctx context.Context, c *lsp.Client, sent *lspSent) (any, error) {
		if sent == nil || sent.version != version {
			return nil, errLSPNotSynced
		}

		pos := lsp.Position{Line: line, Character: col}
		if line >= 0 && line < len(sent.lines) {
			pos = lsp.PositionFromRunes(sent.lines[line], line, col)
		}

		return work(ctx, c, uri, pos)
	}, func(ctx context.Context, res any, err error) bool {
		cur := m.docs[path]
		if cur == nil || cur.server != srv {
			return false
		}

		return done(ctx, res, err, cur.version == version && cur.pendingAt.IsZero())
	})
}

// callServer runs work against srv without a document (workspace-level
// requests). uri names the document whose sent text is passed to work.
func (m *lspManager) callServer(srv *lspServer, uri string,
	work func(ctx context.Context, c *lsp.Client, sent *lspSent) (any, error),
	done func(ctx context.Context, res any, err error) bool,
) {
	srv.enqueue(lspOp{kind: lspOpCall, uri: uri, call: func(c *lsp.Client, sent *lspSent) {
		m.wg.Add(1)

		go func() {
			defer m.wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), lspReqTimeout)
			defer cancel()

			res, err := work(ctx, c, sent)
			if err != nil && !errors.Is(err, errLSPNotSynced) {
				m.log.addf("[%s] request: %v", srv.cfg.Name, err)
			}

			m.post(lspEvent{kind: lspEvFunc, server: srv, fn: func(ctx context.Context) bool {
				return done(ctx, res, err)
			}})
		}()
	}})
}

// anyServer returns the server of the active document, else any running one.
func (m *lspManager) anyServer() (*lspServer, string) {
	a := m.app

	if ld := m.docs[a.activeDocPath()]; ld != nil && ld.server != nil {
		return ld.server, ld.uri
	}

	for _, ld := range m.docs {
		if ld.server != nil {
			return ld.server, ld.uri
		}
	}

	return nil, ""
}
