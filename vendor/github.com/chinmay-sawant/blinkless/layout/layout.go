package layout

import ilayout "github.com/chinmay-sawant/blinkless/internal/layout"

// ptToPx converts a layout point to one CSS pixel at zoom 1.
const ptToPx = 96.0 / 72.0

// Box is one element border box in CSS pixels.
// Action is the data-action attribute. Text is the descendant text.
type Box struct {
	ID     string
	Tag    string
	Action string
	Text   string
	// ParentIndex is the index of this element's nearest element ancestor in
	// Display.Boxes. A top-level element uses -1.
	ParentIndex int
	// OperationIDs identifies paint operations emitted by this element's
	// subtree. A host can use these IDs to capture the element without guessing
	// ownership from overlapping rectangles.
	OperationIDs        []uint64
	Animations          []Animation
	Transitions         []Transition
	TriggerScope        string
	ViewTransitionName  string
	ViewTransitionClass string
	ViewTransitionGroup string
	ViewTransitionScope string
	// Timelines holds the named scroll and view timelines this element
	// declares. Scroll comes before view; a nil slice means no declaration.
	// See TimelineDef for the resolution rules.
	Timelines []TimelineDef
	// OverflowClip is the accumulated ancestor overflow padding box in CSS
	// pixels, with HasOverflowClip false when no ancestor clips. A host
	// painting this element through a transform intersects the paint with
	// it; geometry-baked clipping alone moves with the matrix.
	OverflowClip    [4]float64
	HasOverflowClip bool
	// Transform carries the resolved transform metadata for this element.
	Transform TransformInfo
	X         float64
	Y         float64
	W         float64
	H         float64
}

// TransformInfo reports the resolved transform metadata for one element box.
// The engine bakes a matrix into every descendant operation; these fields let a
// host separate the element's own transform from its ancestors' and animate it
// without re-running layout.
//
// Parent, OriginX, OriginY, and World are populated for every element box, even
// one whose stylesheet declares no transform, because a keyframe can animate a
// transform on an otherwise untransformed element. HasMatrix reports whether the
// stylesheet declares a static transform; Own is the identity when it does not.
//
// OriginX and OriginY are in layout points, the same unit as Matrix2D's E and F,
// not CSS pixels. Multiply by Display.PointsPerPixel when CSS pixels are needed.
type TransformInfo struct {
	// OriginX and OriginY are the resolved transform-origin in layout points.
	OriginX float64
	OriginY float64
	// Box is the transform-box reference: border-box, content-box, fill-box,
	// stroke-box, or view-box.
	Box string

	// Parent maps the parent coordinate space to device space.
	Parent Matrix2D
	// Own is this element's own baked transform, with its origin applied.
	Own Matrix2D
	// World is Parent * Own, the matrix the engine stamps on this element's ops.
	World Matrix2D

	// HasMatrix reports whether the element declares a transform.
	HasMatrix bool
	// HasTransform3D reports whether the element participates in a 3D
	// composition. A host should use a re-projection pass rather than a 2D
	// matrix for such an element.
	HasTransform3D bool
	// Perspective is the resolved perspective distance in layout points. Zero
	// means none.
	Perspective float64
	// PerspectiveOriginX and PerspectiveOriginY are the resolved vanishing point
	// in layout points.
	PerspectiveOriginX float64
	PerspectiveOriginY float64
	// HasPerspective reports whether the element declares a perspective.
	HasPerspective bool
	// Style is flat or preserve-3d.
	Style string
	// Backface is visible or hidden.
	Backface string
}

// Animation carries one CSS animation list item for a host to sample.
// Times are milliseconds. Blinkless does not advance animation time.
type Animation struct {
	Name              string  `json:"name"`
	DurationMS        float64 `json:"durationMs"`
	DurationAuto      bool    `json:"durationAuto,omitempty"`
	TimingFunction    string  `json:"timingFunction"`
	IterationCount    float64 `json:"iterationCount"`
	IterationInfinite bool    `json:"iterationInfinite,omitempty"`
	Direction         string  `json:"direction"`
	FillMode          string  `json:"fillMode"`
	PlayState         string  `json:"playState"`
	Composition       string  `json:"composition"`
	DelayStartMS      float64 `json:"delayStartMs"`
	DelayEndMS        float64 `json:"delayEndMs"`
	Timeline          string  `json:"timeline"`
	RangeStart        string  `json:"rangeStart"`
	RangeEnd          string  `json:"rangeEnd"`
	RangeCenter       string  `json:"rangeCenter"`
	Trigger           string  `json:"trigger,omitempty"`
}

// Transition carries one CSS transition list item for a host to sample.
// Times are milliseconds. Blinkless does not compare or interpolate frames.
type Transition struct {
	Property       string  `json:"property"`
	DurationMS     float64 `json:"durationMs"`
	DelayMS        float64 `json:"delayMs"`
	TimingFunction string  `json:"timingFunction"`
	Behavior       string  `json:"behavior"`
}

// KeyframesRule is one parsed @keyframes rule. Repeated names remain in
// source order; CSS resolves the last matching name when selecting a rule.
type KeyframesRule struct {
	Name      string     `json:"name"`
	Keyframes []Keyframe `json:"keyframes"`
}

// Keyframe is one selector offset in a keyframes rule. Offset is in [0, 1].
type Keyframe struct {
	Offset       float64               `json:"offset"`
	Declarations []KeyframeDeclaration `json:"declarations"`
}

// KeyframeDeclaration keeps one CSS property and its raw value.
type KeyframeDeclaration struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// Options carries the optional inputs a placement can use beyond the styled
// document itself.
type Options struct {
	// Images returns encoded image bytes (PNG, JPEG, or SVG) for one source,
	// such as an <img src> value or a CSS background-image url(...) target.
	// Nil means no source resolves, so image paint is skipped. A transform
	// that cannot stay in those source bytes is re-encoded as a PNG on the
	// image operation. That payload is the bitmap fallback. It is not a
	// picture of the page.
	Images func(src string) ([]byte, error)

	// RetainState keeps the layout state an element patch needs: the
	// resolved styles of the document and the box tree the placement
	// produced. A display that carries that state can be patched by
	// RelayoutElement.
	//
	// It is off by default. Without it a display keeps nothing beyond the
	// operations and the boxes a host draws, so a host that never patches
	// an element does not pay for the retained state. A host that patches
	// sets it on the DisplayListOptions call that builds the display it
	// patches, and RelayoutElement refuses a display that was laid out
	// without it.
	RetainState bool
}

// Override replaces one layout property of one element for a single
// RelayoutElementWithOverrides call.
//
// The override is applied to the element's used style after the cascade and
// before placement. It is not written to the parsed tree and is not kept in
// the cached style, so the next static frame is the one the stylesheet
// produces. Inherited properties reach the element's descendants as usual; a
// non-inherited property changes only the element.
//
// ElementID may be empty to name the element passed to
// RelayoutElementWithOverrides. A non-empty ID must name that element; a
// different element cannot be overridden through this call.
//
// Property is a CSS property name, and Value is a CSS value. The path accepts
// the layout tier: sizes and their logical forms, insets, margins, padding,
// gap, flex and grid placement, aspect-ratio, border and outline geometry,
// text metrics, and object-position. A paint property, a transform, an SVG
// paint value, a display or flow property, a custom property, and a value that
// is a CSS-wide keyword or contains var() are refused with
// ErrUnknownOverrideProperty or ErrInvalidOverrideValue.
//
// When one list names a shorthand and one of its longhands, the longhand wins,
// the way the cascade applies them. When it names the same property twice, the
// later value wins.
type Override struct {
	// ElementID names the element the override applies to. Empty means
	// the element passed to RelayoutElementWithOverrides.
	ElementID string

	// Property is the CSS property name to replace.
	Property string

	// Value is the CSS value to use for this pass.
	Value string
}

func boxesFrom(placed []ilayout.PlacedElement) []Box {
	boxes := make([]Box, len(placed))

	for i, item := range placed {
		boxes[i] = Box{
			ID:                  item.ID,
			Tag:                 item.Tag,
			Action:              item.Action,
			Text:                item.Text,
			ParentIndex:         item.ParentIndex,
			OperationIDs:        item.OperationIDs,
			Animations:          animationsFrom(item.Animations),
			Transitions:         transitionsFrom(item.Transitions),
			TriggerScope:        item.TriggerScope,
			ViewTransitionName:  item.ViewTransitionName,
			ViewTransitionClass: item.ViewTransitionClass,
			ViewTransitionGroup: item.ViewTransitionGroup,
			ViewTransitionScope: item.ViewTransitionScope,
			Timelines:           timelinesFrom(item.Timelines),
			Transform:           transformInfoFrom(item.Transform),
			OverflowClip: [4]float64{
				item.OverflowClip[0] * ptToPx,
				item.OverflowClip[1] * ptToPx,
				item.OverflowClip[2] * ptToPx,
				item.OverflowClip[3] * ptToPx,
			},
			HasOverflowClip: item.HasOverflowClip,
			X:               item.X * ptToPx,
			Y:               item.Y * ptToPx,
			W:               item.W * ptToPx,
			H:               item.H * ptToPx,
		}
	}

	return boxes
}

// transformInfoFrom maps the engine-side transform metadata to the public one.
// Matrix2D is an alias, so the matrices assign directly.
func transformInfoFrom(item ilayout.TransformInfo) TransformInfo {
	return TransformInfo{
		OriginX:            item.OriginX,
		OriginY:            item.OriginY,
		Box:                item.Box,
		Parent:             item.Parent,
		Own:                item.Own,
		World:              item.World,
		HasMatrix:          item.HasMatrix,
		HasTransform3D:     item.HasTransform3D,
		Perspective:        item.Perspective,
		PerspectiveOriginX: item.PerspectiveOriginX,
		PerspectiveOriginY: item.PerspectiveOriginY,
		HasPerspective:     item.HasPerspective,
		Style:              item.Style,
		Backface:           item.Backface,
	}
}

func animationsFrom(records []ilayout.AnimationRecord) []Animation {
	if len(records) == 0 {
		return nil
	}

	items := make([]Animation, len(records))
	for i, record := range records {
		items[i] = Animation{
			Name:              unquoteAnimationName(record.Name),
			DurationMS:        record.DurationMS,
			DurationAuto:      record.DurationAuto,
			TimingFunction:    record.TimingFunction,
			IterationCount:    record.IterationCount,
			IterationInfinite: record.IterationInfinite,
			Direction:         record.Direction,
			FillMode:          record.FillMode,
			PlayState:         record.PlayState,
			Composition:       record.Composition,
			DelayStartMS:      record.DelayStartMS,
			DelayEndMS:        record.DelayEndMS,
			Timeline:          record.Timeline,
			RangeStart:        record.RangeStart,
			RangeEnd:          record.RangeEnd,
			RangeCenter:       record.RangeCenter,
			Trigger:           record.Trigger,
		}
	}

	return items
}

func unquoteAnimationName(name string) string {
	if len(name) >= 2 && (name[0] == '\'' || name[0] == '"') && name[len(name)-1] == name[0] {
		return name[1 : len(name)-1]
	}

	return name
}

func transitionsFrom(records []ilayout.TransitionRecord) []Transition {
	if len(records) == 0 {
		return nil
	}

	items := make([]Transition, len(records))
	for i, record := range records {
		items[i] = Transition{
			Property:       record.Property,
			DurationMS:     record.DurationMS,
			DelayMS:        record.DelayMS,
			TimingFunction: record.TimingFunction,
			Behavior:       record.Behavior,
		}
	}

	return items
}
