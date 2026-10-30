package css

// StatePseudos reports which interactive pseudo-classes any selector in the
// sheets can match. A page whose sheets never mention :hover, :active, or
// :focus cannot change appearance for those states, so a host can skip the
// redraw that recording them would otherwise trigger.
type StatePseudos struct {
	Hover  bool
	Active bool
	Focus  bool
}

// UsesStatePseudos scans every selector, including :is(), :not(), and :has()
// arguments, for the interactive state pseudo-classes.
func UsesStatePseudos(sheets []*Stylesheet) StatePseudos {
	var out StatePseudos

	for _, sheet := range sheets {
		if sheet == nil {
			continue
		}

		for i := range sheet.Rules {
			for _, sel := range sheet.Rules[i].Selectors {
				scanSelectorState(sel, &out)
			}
		}
	}

	return out
}

// scanSelectorState folds one selector's pseudo-class state into out.
func scanSelectorState(sel Selector, out *StatePseudos) {
	for _, part := range sel.Parts {
		for _, pseudo := range part.Pseudos {
			switch pseudo.Name {
			case pseudoClassHover:
				out.Hover = true
			case pseudoClassActive:
				out.Active = true
			case pseudoClassFocus, pseudoClassFocusVisible, "focus-within":
				out.Focus = true
			}

			for _, nested := range pseudo.Not {
				scanSelectorState(nested, out)
			}

			for _, nested := range pseudo.Is {
				scanSelectorState(nested, out)
			}

			for _, relative := range pseudo.Has {
				scanSelectorState(Selector{Parts: relative.Parts}, out)
			}
		}
	}
}
