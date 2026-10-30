package css

import icss "github.com/chinmay-sawant/blinkless/internal/css"

// StatePseudos reports which interactive pseudo-classes any rule can match.
type StatePseudos = icss.StatePseudos

// UsesStatePseudo reports whether the document's stylesheets can match the
// named interactive pseudo-class ("hover", "active", or "focus", which also
// covers focus-visible and focus-within). A host uses it to skip a redraw for
// state that cannot change paint. An unknown name or a nil document answers
// true, so a caller that does not understand the state keeps the redraw.
func (d *Document) UsesStatePseudo(name string) bool {
	if d == nil {
		return true
	}

	states := icss.UsesStatePseudos(d.sheets)

	switch name {
	case "hover":
		return states.Hover
	case "active":
		return states.Active
	case "focus":
		return states.Focus
	}

	return true
}
