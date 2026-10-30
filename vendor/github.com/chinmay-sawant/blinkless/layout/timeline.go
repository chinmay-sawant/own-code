package layout

import ilayout "github.com/chinmay-sawant/blinkless/internal/layout"

// TimelineDef is one named timeline an element box declares for a host to
// resolve. CSS Scroll-driven Animations declares it with scroll-timeline or
// view-timeline, and animation-timeline references it by name. A box can
// declare both kinds, so it can carry one scroll definition and one view
// definition in that order.
//
// The engine owns no clock and no scroll offset: it does not compute timeline
// progress. A host resolves the name against the nearest ancestor box, then
// reads the page scroll state itself.
//
// The scroller is always the page scroller in this engine, so ScrollerID is
// empty. Nested element scroll containers and timeline-scope are not
// implemented; a name that would need either stays unresolved. One name per
// kind is modeled, so a comma-separated name list is rejected at parse time.
type TimelineDef struct {
	// Name is the dashed ident the element declares, such as --progress.
	Name string
	// Kind is "scroll" or "view".
	Kind string
	// Axis is the used physical scroll axis: "x" or "y". A block or inline
	// declaration is resolved through the element's writing mode.
	Axis string
	// SubjectID is the id of the element a view timeline observes. A scroll
	// timeline has no subject, so it is empty.
	SubjectID string
	// ScrollerID is the scroll container the timeline reads. Empty means the
	// page scroller.
	ScrollerID string
	// InsetStart and InsetEnd are the view timeline insets in CSS pixels, the
	// same unit as Box.X/Y/W/H. They are zero when the declaration is auto or
	// absent.
	InsetStart float64
	InsetEnd   float64
}

// timelinesFrom maps the engine-side definitions to the public payload. A box
// with no declaration keeps a nil slice. Insets convert from layout points to
// CSS pixels with the border box, so a host compares them with Box.X/Y/W/H
// directly.
func timelinesFrom(defs []ilayout.TimelineDef) []TimelineDef {
	if len(defs) == 0 {
		return nil
	}

	timelines := make([]TimelineDef, len(defs))

	for i, def := range defs {
		timelines[i] = TimelineDef{
			Name:       def.Name,
			Kind:       def.Kind,
			Axis:       def.Axis,
			SubjectID:  def.SubjectID,
			ScrollerID: def.ScrollerID,
			InsetStart: def.InsetStart * ptToPx,
			InsetEnd:   def.InsetEnd * ptToPx,
		}
	}

	return timelines
}
