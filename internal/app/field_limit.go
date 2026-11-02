package app

// clampFieldLen truncates a single-line input to maxFieldLen runes. The
// templates carry maxlength="255" so the engine clamps typing and paste;
// this covers restored sessions and programmatic sets on the Go side.
func clampFieldLen(s string) string {
	r := []rune(s)
	if len(r) <= maxFieldLen {
		return s
	}

	return string(r[:maxFieldLen])
}

// filterDisplayBudget caps the explorer filter's painted width so a long
// unbroken value stays inside its box. The input is ~190 px wide at the
// base 268 px drawer (250 container minus 16 padding, 12 icon, 14 gaps,
// 18 clear button); at a 6.5 px advance that fits ~29 runes. The engine
// paints long labels past their measured box (see clipCmdbarText), so the
// CSS ellipsis trio alone cannot hold them and the display string is
// clipped in Go with a trailing ellipsis. The full filter stays in a.filter
// for matching; the binder reconciles the display value on the next edit.
const filterDisplayBudget = 28

// clipFilterDisplay keeps the filter's painted value on one line within
// the filter box, marking the cut with an ellipsis. Short values pass
// through unchanged.
func clipFilterDisplay(s string) string {
	r := []rune(s)
	if len(r) <= filterDisplayBudget {
		return s
	}

	if filterDisplayBudget <= 1 {
		return "…"
	}

	return string(r[:filterDisplayBudget-1]) + "…"
}
