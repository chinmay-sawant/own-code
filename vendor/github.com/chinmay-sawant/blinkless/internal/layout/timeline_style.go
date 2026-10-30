package layout

import "strings"

// Named timeline declaration names (CSS Scroll-driven Animations 1). The
// scroll and view shorthands expand into the longhands here, at cascade time,
// so a shorthand and a longhand compete per property with each declaration's
// own origin, specificity, and source order.
const (
	propScrollTimeline     = "scroll-timeline"
	propScrollTimelineName = "scroll-timeline-name"
	propScrollTimelineAxis = "scroll-timeline-axis"
	propViewTimeline       = "view-timeline"
	propViewTimelineName   = "view-timeline-name"
	propViewTimelineAxis   = "view-timeline-axis"
	propViewTimelineInset  = "view-timeline-inset"

	timelineAxisBlock  = "block"
	timelineAxisInline = "inline"
	timelineAxisX      = "x"
	timelineAxisY      = "y"

	timelineKindScroll = "scroll"
	timelineKindView   = "view"

	timelineAutoKeyword           = "auto"
	timelineNoneKeyword           = "none"
	timelineInsetMaxCount         = 2
	timelineScrollShorthandTokens = 2
	timelineViewShorthandTokens   = 4
	timelineDefCapacity           = 2
)

// TimelineDef is one named timeline an element declares for a host to resolve.
// A box can declare both kinds, so one element can export two definitions.
// The scroller is always the page scroller in this engine, so ScrollerID stays
// empty; a nested element scroll container cannot be named yet.
type TimelineDef struct {
	// Name is the dashed ident the element declares, such as --progress.
	Name string
	// Kind is timelineKindScroll or timelineKindView.
	Kind string
	// Axis is the used physical scroll axis: x or y. A block or inline
	// declaration is resolved through the element's writing mode.
	Axis string
	// SubjectID is the id of the element a view timeline observes. A scroll
	// timeline has no subject, so it stays empty.
	SubjectID string
	// ScrollerID is the scroll container the timeline reads. Empty means the
	// page scroller, the only scroller this engine models.
	ScrollerID string
	// InsetStart and InsetEnd are the view timeline insets in layout points.
	// They are zero when the declaration is auto or absent.
	InsetStart float64
	InsetEnd   float64
}

// timelineDefsFromStyle builds the named timeline definitions one element
// exports. Scroll comes before view so the order is stable across redraws. A
// nil style means the element declares no timeline.
func timelineDefsFromStyle(style *ResolvedStyle, subjectID string) []TimelineDef {
	if style == nil || style.ScrollTimelineName == "" && style.ViewTimelineName == "" {
		return nil
	}

	defs := make([]TimelineDef, 0, timelineDefCapacity)

	if style.ScrollTimelineName != "" {
		defs = append(defs, TimelineDef{
			Name:       style.ScrollTimelineName,
			Kind:       timelineKindScroll,
			Axis:       timelineUsedAxis(style.ScrollTimelineAxis, style.WritingMode),
			SubjectID:  "",
			ScrollerID: "",
			InsetStart: 0,
			InsetEnd:   0,
		})
	}

	if style.ViewTimelineName != "" {
		defs = append(defs, TimelineDef{
			Name:       style.ViewTimelineName,
			Kind:       timelineKindView,
			Axis:       timelineUsedAxis(style.ViewTimelineAxis, style.WritingMode),
			SubjectID:  subjectID,
			ScrollerID: "",
			InsetStart: style.ViewTimelineInsetStart,
			InsetEnd:   style.ViewTimelineInsetEnd,
		})
	}

	return defs
}

// timelineProperty reports whether prop is one of the named timeline
// declarations this engine parses.
func timelineProperty(prop string) bool {
	switch prop {
	case propScrollTimeline, propScrollTimelineName, propScrollTimelineAxis,
		propViewTimeline, propViewTimelineName, propViewTimelineAxis, propViewTimelineInset:
		return true
	default:
		return false
	}
}

// timelineDeclarationValueAccepted is the cascade gate for the timeline
// declarations. A CSS-wide keyword and a var() reference are valid at parse
// time; every other value must match the grammar.
func timelineDeclarationValueAccepted(prop, value string) (bool, bool) {
	if !timelineProperty(prop) {
		return false, false
	}

	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) || containsVarFunc(trimmed) {
		return true, true
	}

	switch prop {
	case propScrollTimeline:
		_, _, ok := parseScrollTimelineShorthand(trimmed)

		return ok, true
	case propViewTimeline:
		_, _, _, ok := parseViewTimelineShorthand(trimmed)

		return ok, true
	case propScrollTimelineName, propViewTimelineName:
		_, ok := parseTimelineName(trimmed)

		return ok, true
	case propScrollTimelineAxis, propViewTimelineAxis:
		_, ok := parseTimelineAxis(trimmed)

		return ok, true
	default:
		_, _, ok := parseViewTimelineInset(trimmed)

		return ok, true
	}
}

// parseTimelineName reads one timeline name: the none keyword or a dashed
// ident. This engine stores one name per kind, so a comma list of names is
// rejected rather than silently narrowed to its first item.
func parseTimelineName(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, timelineNoneKeyword) {
		return "", true
	}

	if animationDashedIdent(trimmed) {
		return trimmed, true
	}

	return "", false
}

// parseTimelineAxis reads one axis keyword: block, inline, x, or y. The
// returned keyword is lower-case and stays logical for block and inline; the
// writing mode picks the physical axis at export.
func parseTimelineAxis(value string) (string, bool) {
	axis := strings.ToLower(strings.TrimSpace(value))

	switch axis {
	case timelineAxisBlock, timelineAxisInline, timelineAxisX, timelineAxisY:
		return axis, true
	default:
		return "", false
	}
}

// timelineUsedAxis resolves a declared axis keyword to the scroll axis the
// host reads: block and inline follow the writing mode, as the engine's
// logical property mapping does.
func timelineUsedAxis(axis, writingMode string) string {
	vertical := isVerticalWritingMode(writingMode)

	switch strings.ToLower(axis) {
	case timelineAxisX:
		return timelineAxisX
	case timelineAxisY:
		return timelineAxisY
	case timelineAxisInline:
		if vertical {
			return timelineAxisY
		}

		return timelineAxisX
	default: // the initial block axis
		if vertical {
			return timelineAxisX
		}

		return timelineAxisY
	}
}

// parseScrollTimelineShorthand reads <name> <axis>?. The name comes first, as
// the grammar requires. A missing axis resets the longhand to the initial
// block axis.
func parseScrollTimelineShorthand(value string) (string, string, bool) {
	tokens := strings.Fields(value)
	if len(tokens) == 0 || len(tokens) > timelineScrollShorthandTokens {
		return "", "", false
	}

	if _, ok := parseTimelineName(tokens[0]); !ok {
		return "", "", false
	}

	axis := timelineAxisBlock

	if len(tokens) == timelineScrollShorthandTokens {
		parsed, ok := parseTimelineAxis(tokens[1])
		if !ok {
			return "", "", false
		}

		axis = parsed
	}

	return tokens[0], axis, true
}

// parseViewTimelineShorthand reads <name> [ <axis> || <inset> ]?. The axis
// keyword may appear before or after the one or two inset values. A missing
// axis or inset keeps the longhand initial value, so the shorthand resets a
// previous longhand from a weaker origin.
func parseViewTimelineShorthand(value string) (string, string, string, bool) {
	tokens := strings.Fields(value)
	if len(tokens) == 0 || len(tokens) > timelineViewShorthandTokens {
		return "", "", "", false
	}

	if _, ok := parseTimelineName(tokens[0]); !ok {
		return "", "", "", false
	}

	axis, axisSeen := timelineAxisBlock, false
	insets := make([]string, 0, timelineInsetMaxCount)

	for _, token := range tokens[1:] {
		if parsed, ok := parseTimelineAxis(token); ok {
			if axisSeen {
				return "", "", "", false
			}

			axis, axisSeen = parsed, true

			continue
		}

		insets = append(insets, token)
	}

	inset := timelineAutoKeyword

	if len(insets) > 0 {
		inset = strings.Join(insets, " ")
		if _, _, ok := parseViewTimelineInset(inset); !ok {
			return "", "", "", false
		}
	}

	return tokens[0], axis, inset, true
}

// parseViewTimelineInset splits the one or two inset values. One value sets
// both edges, matching the property grammar.
func parseViewTimelineInset(value string) (string, string, bool) {
	tokens := strings.Fields(value)
	if len(tokens) == 0 || len(tokens) > timelineInsetMaxCount {
		return "", "", false
	}

	for _, token := range tokens {
		if !timelineInsetTokenAccepted(token) {
			return "", "", false
		}
	}

	if len(tokens) == 1 {
		return tokens[0], tokens[0], true
	}

	return tokens[0], tokens[1], true
}

// timelineInsetTokenAccepted reports whether one inset token is auto or a
// length-percentage. Negative values are legal: they move the range outside
// the scrollport.
func timelineInsetTokenAccepted(token string) bool {
	if strings.EqualFold(token, timelineAutoKeyword) {
		return true
	}

	return animationLengthPercentageValueAccepted(token)
}

// expandScrollTimelineDeclaration expands the winning scroll-timeline
// shorthand into its name and axis longhands.
func expandScrollTimelineDeclaration(prop, value string) ([]logicalPropDecl, bool) {
	if prop != propScrollTimeline {
		return nil, false
	}

	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return []logicalPropDecl{
			{prop: propScrollTimelineName, val: trimmed},
			{prop: propScrollTimelineAxis, val: trimmed},
		}, true
	}

	name, axis, ok := parseScrollTimelineShorthand(trimmed)
	if !ok {
		return nil, false
	}

	return []logicalPropDecl{
		{prop: propScrollTimelineName, val: name},
		{prop: propScrollTimelineAxis, val: axis},
	}, true
}

// expandViewTimelineDeclaration expands the winning view-timeline shorthand
// into its name, axis, and inset longhands.
func expandViewTimelineDeclaration(prop, value string) ([]logicalPropDecl, bool) {
	if prop != propViewTimeline {
		return nil, false
	}

	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		return []logicalPropDecl{
			{prop: propViewTimelineName, val: trimmed},
			{prop: propViewTimelineAxis, val: trimmed},
			{prop: propViewTimelineInset, val: trimmed},
		}, true
	}

	name, axis, inset, ok := parseViewTimelineShorthand(trimmed)
	if !ok {
		return nil, false
	}

	return []logicalPropDecl{
		{prop: propViewTimelineName, val: name},
		{prop: propViewTimelineAxis, val: axis},
		{prop: propViewTimelineInset, val: inset},
	}, true
}

// applyTimelineProps resolves the named timeline declarations. The shorthands
// reach this handler only through var() substitution; a literal shorthand is
// already expanded at cascade time.
func applyTimelineProps(
	style *ResolvedStyle, prop, value string, fsize float64, ctx *styleContext,
	parent *ResolvedStyle, hasParent bool,
) bool {
	if !timelineProperty(prop) {
		return false
	}

	applyTimelineProp(style, prop, value, fsize, ctx, parent, hasParent)

	return true
}

func applyTimelineProp(
	style *ResolvedStyle, prop, value string, fsize float64, ctx *styleContext,
	parent *ResolvedStyle, hasParent bool,
) {
	switch prop {
	case propScrollTimeline, propViewTimeline:
		applyTimelineShorthand(style, prop, value, fsize, ctx, parent, hasParent)
	case propScrollTimelineName, propViewTimelineName:
		applyTimelineNameProp(style, prop, value, parent, hasParent)
	case propScrollTimelineAxis, propViewTimelineAxis:
		applyTimelineAxisProp(style, prop, value, parent, hasParent)
	case propViewTimelineInset:
		applyViewTimelineInsetProp(style, value, fsize, ctx, parent, hasParent)
	}
}

func applyTimelineShorthand(
	style *ResolvedStyle, prop, value string, fsize float64, ctx *styleContext,
	parent *ResolvedStyle, hasParent bool,
) {
	var (
		declarations []logicalPropDecl
		valid        bool
	)

	if prop == propScrollTimeline {
		declarations, valid = expandScrollTimelineDeclaration(prop, value)
	} else {
		declarations, valid = expandViewTimelineDeclaration(prop, value)
	}

	if !valid {
		return
	}

	for _, declaration := range declarations {
		applyTimelineProp(style, declaration.prop, declaration.val, fsize, ctx, parent, hasParent)
	}
}

// applyTimelineNameProp stores one name longhand. Neither name is inherited,
// so only inherit copies the parent value; the other wide keywords reset to
// none.
func applyTimelineNameProp(style *ResolvedStyle, prop, value string, parent *ResolvedStyle, hasParent bool) {
	var parentValue string

	if prop == propScrollTimelineName {
		parentValue = parentScrollTimelineName(parent, hasParent)
	} else {
		parentValue = parentViewTimelineName(parent, hasParent)
	}

	trimmed := strings.TrimSpace(value)

	var name string

	switch {
	case cssWideKeyword(strings.ToLower(trimmed)):
		name = timelineNameWideValue(strings.ToLower(trimmed), parentValue, hasParent)
	default:
		parsed, valid := parseTimelineName(trimmed)
		if !valid {
			return
		}

		name = parsed
	}

	if prop == propScrollTimelineName {
		style.ScrollTimelineName = name
	} else {
		style.ViewTimelineName = name
	}
}

// applyTimelineAxisProp stores one axis longhand. The initial block axis is
// re-applied for every wide keyword except inherit.
func applyTimelineAxisProp(style *ResolvedStyle, prop, value string, parent *ResolvedStyle, hasParent bool) {
	var parentValue string

	if prop == propScrollTimelineAxis {
		parentValue = parentScrollTimelineAxis(parent, hasParent)
	} else {
		parentValue = parentViewTimelineAxis(parent, hasParent)
	}

	trimmed := strings.TrimSpace(value)

	var axis string

	switch {
	case cssWideKeyword(strings.ToLower(trimmed)):
		axis = timelineAxisWideValue(strings.ToLower(trimmed), parentValue, hasParent)
	default:
		parsed, valid := parseTimelineAxis(trimmed)
		if !valid {
			return
		}

		axis = parsed
	}

	if prop == propScrollTimelineAxis {
		style.ScrollTimelineAxis = axis
	} else {
		style.ViewTimelineAxis = axis
	}
}

// applyViewTimelineInsetProp stores the start and end insets in layout points.
// auto leaves a Set flag false; percentages resolve against the page
// scrollport dimension the timeline axis reads.
func applyViewTimelineInsetProp(
	style *ResolvedStyle, value string, fsize float64, ctx *styleContext,
	parent *ResolvedStyle, hasParent bool,
) {
	trimmed := strings.TrimSpace(value)
	if cssWideKeyword(strings.ToLower(trimmed)) {
		applyViewTimelineInsetWide(style, strings.ToLower(trimmed), parent, hasParent)

		return
	}

	startToken, endToken, ok := parseViewTimelineInset(trimmed)
	if !ok {
		return
	}

	scrollport := timelineScrollport(style, ctx)
	style.ViewTimelineInsetStart, style.ViewTimelineInsetStartSet = timelineInsetValue(startToken, fsize, scrollport)
	style.ViewTimelineInsetEnd, style.ViewTimelineInsetEndSet = timelineInsetValue(endToken, fsize, scrollport)
}

// timelineInsetValue resolves one inset token. auto resolves to zero with a
// false Set flag so the host can tell it apart from an explicit 0px.
func timelineInsetValue(token string, fsize, scrollport float64) (float64, bool) {
	if strings.EqualFold(token, timelineAutoKeyword) {
		return 0, false
	}

	if points, ok := calcLength(token, fsize, scrollport); ok {
		return points, true
	}

	if points, ok := plainLength(token, fsize, scrollport); ok {
		return points, true
	}

	return 0, false
}

// timelineScrollport is the page scrollport dimension on the view timeline
// axis, in layout points. Percentages resolve against it.
func timelineScrollport(style *ResolvedStyle, ctx *styleContext) float64 {
	if ctx == nil {
		return 0
	}

	if timelineUsedAxis(style.ViewTimelineAxis, style.WritingMode) == timelineAxisX {
		return ctx.viewportW
	}

	return ctx.viewportH
}

func applyViewTimelineInsetWide(style *ResolvedStyle, keyword string, parent *ResolvedStyle, hasParent bool) {
	style.ViewTimelineInsetStart = 0
	style.ViewTimelineInsetEnd = 0
	style.ViewTimelineInsetStartSet = false
	style.ViewTimelineInsetEndSet = false

	if keyword == inheritKeyword && hasParent && parent != nil {
		style.ViewTimelineInsetStart = parent.ViewTimelineInsetStart
		style.ViewTimelineInsetEnd = parent.ViewTimelineInsetEnd
		style.ViewTimelineInsetStartSet = parent.ViewTimelineInsetStartSet
		style.ViewTimelineInsetEndSet = parent.ViewTimelineInsetEndSet
	}
}

// timelineNameWideValue resolves a wide keyword for a non-inherited name.
func timelineNameWideValue(keyword, parentValue string, hasParent bool) string {
	if keyword == inheritKeyword && hasParent {
		return parentValue
	}

	return ""
}

// timelineAxisWideValue resolves a wide keyword for a non-inherited axis. The
// initial value is the block axis.
func timelineAxisWideValue(keyword, parentValue string, hasParent bool) string {
	if keyword == inheritKeyword && hasParent && parentValue != "" {
		return parentValue
	}

	return timelineAxisBlock
}

func parentScrollTimelineName(parent *ResolvedStyle, hasParent bool) string {
	if !hasParent || parent == nil {
		return ""
	}

	return parent.ScrollTimelineName
}

func parentScrollTimelineAxis(parent *ResolvedStyle, hasParent bool) string {
	if !hasParent || parent == nil {
		return ""
	}

	return parent.ScrollTimelineAxis
}

func parentViewTimelineName(parent *ResolvedStyle, hasParent bool) string {
	if !hasParent || parent == nil {
		return ""
	}

	return parent.ViewTimelineName
}

func parentViewTimelineAxis(parent *ResolvedStyle, hasParent bool) string {
	if !hasParent || parent == nil {
		return ""
	}

	return parent.ViewTimelineAxis
}
