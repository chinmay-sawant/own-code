package agentreview

import (
	"fmt"
	"strings"
)

// Unified renders the file's hunks as a git-style unified diff that the
// app's existing diff parser understands. Hunks that are not pending are
// omitted unless includeAll. The header carries session and file identity as
// diff metadata lines the parser ignores.
func (fr *FileReview) Unified(includeAll bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "diff --git a/%s b/%s\n", fr.Path, fr.Path)
	if fr.Base == "" {
		sb.WriteString("new file mode 100644\n--- /dev/null\n")
	} else {
		fmt.Fprintf(&sb, "--- a/%s\n", fr.Path)
	}
	fmt.Fprintf(&sb, "+++ b/%s\n", fr.Path)
	delta := 0
	for i, h := range fr.Hunks {
		if !includeAll && fr.States[i] != Pending {
			delta += len(h.New) - len(h.Old)
			continue
		}
		start := h.OldStart + 1
		if len(h.Old) == 0 {
			start = h.OldStart
		}
		ns := h.OldStart + delta + 1
		if len(h.New) == 0 {
			ns = h.OldStart + delta
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", start, len(h.Old), ns, len(h.New))
		for _, l := range h.Old {
			sb.WriteString("-" + strings.TrimSuffix(l, "\n") + "\n")
		}
		for _, l := range h.New {
			sb.WriteString("+" + strings.TrimSuffix(l, "\n") + "\n")
		}
		delta += len(h.New) - len(h.Old)
	}
	return sb.String()
}
