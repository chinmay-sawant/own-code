package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/lsp"
)

// lspExt groups the per-feature state hung off the manager so lsp_sync.go
// needs only one field.
type lspExt struct {
	cmp  completionState
	sym  symbolState
	refs refsState
	// prog is the live $/progress work per server, keyed by token.
	prog map[*lspServer]map[string]lsp.Progress
}

// onProgress runs on the client's read goroutine: it parses the report and
// hands it to the loop.
func (s *lspServer) onProgress(params json.RawMessage) {
	p, ok := lsp.ParseProgress(params)
	if !ok {
		return
	}

	m := s.mgr

	m.post(lspEvent{kind: lspEvFunc, server: s, fn: func(context.Context) bool {
		return m.applyProgress(s, p)
	}})
}

// applyProgress records one report on the loop and reports a visible change.
func (m *lspManager) applyProgress(s *lspServer, p lsp.Progress) bool {
	if m.servers[s.key] != s {
		return false
	}

	if m.ext.prog == nil {
		m.ext.prog = map[*lspServer]map[string]lsp.Progress{}
	}

	tokens := m.ext.prog[s]
	if tokens == nil {
		tokens = map[string]lsp.Progress{}
		m.ext.prog[s] = tokens
	}

	prev := tokens[p.Token]

	switch p.Kind {
	case "begin":
		tokens[p.Token] = p
	case "report":
		cur, ok := tokens[p.Token]
		if !ok {
			return false
		}

		if p.Message != "" {
			cur.Message = p.Message
		}

		if p.Percent >= 0 {
			cur.Percent = p.Percent
		}

		tokens[p.Token] = cur
	case "end":
		delete(tokens, p.Token)

		// Work finished (for example indexing): ask for symbols again, the
		// first answer may have been empty while the server loaded.
		for path, ds := range m.ext.sym.doc {
			if ld := m.docs[path]; ld != nil && ld.server == s && !ds.ok {
				ds.asked = 0
			}
		}

		if len(tokens) == 0 {
			delete(m.ext.prog, s)
		}

		return true
	default:
		return false
	}

	return tokens[p.Token] != prev
}

// progressText is the status-bar line: "gopls: Indexing 45%" for the first
// busy server, with a "+N" suffix when more work is running.
func (m *lspManager) progressText() string {
	type entry struct {
		name string
		p    lsp.Progress
	}

	var all []entry

	for srv, tokens := range m.ext.prog {
		if m.servers[srv.key] != srv {
			delete(m.ext.prog, srv)

			continue
		}

		for _, p := range tokens {
			all = append(all, entry{srv.cfg.Name, p})
		}
	}

	if len(all) == 0 {
		return ""
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].name != all[j].name {
			return all[i].name < all[j].name
		}

		return all[i].p.Token < all[j].p.Token
	})

	e := all[0]
	parts := []string{e.p.Title}

	if e.p.Message != "" {
		parts = append(parts, e.p.Message)
	}

	text := e.name + ": " + strings.TrimSpace(strings.Join(parts, " "))
	if e.p.Percent >= 0 {
		text += fmt.Sprintf(" %d%%", e.p.Percent)
	}

	if len(all) > 1 {
		text += fmt.Sprintf(" (+%d)", len(all)-1)
	}

	return text
}
