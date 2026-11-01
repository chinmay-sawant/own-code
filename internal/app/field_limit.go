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
