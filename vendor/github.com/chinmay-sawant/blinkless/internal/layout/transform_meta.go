package layout

// TransformInfo reports the resolved transform metadata for one placed
// element. The stamp walk bakes a matrix into every operation in the element's
// subtree; these fields let a caller separate the element's own transform from
// its ancestors' without re-running the cascade or the stamp.
//
// OriginX, OriginY, Parent, Own, World, Perspective, and the perspective origin
// are in layout points, the same unit as Matrix2D's E and F. The element box in
// PlacedElement is also in points; the public layout.Box converts it to CSS
// pixels but keeps these values in points.
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
	// World is Parent * Own, the matrix the stamp walk applies to the
	// element's exclusive operations on the flat path.
	World Matrix2D

	// HasMatrix reports whether the element declares a transform.
	HasMatrix bool
	// HasTransform3D reports whether the element's transform list carries a 3D
	// function.
	HasTransform3D bool
	// Perspective is the resolved perspective distance in layout points. Zero
	// means none.
	Perspective float64
	// PerspectiveOriginX and PerspectiveOriginY are the resolved vanishing
	// point in layout points.
	PerspectiveOriginX float64
	PerspectiveOriginY float64
	// HasPerspective reports whether the element declares a perspective.
	HasPerspective bool
	// Style is flat or preserve-3d.
	Style string
	// Backface is visible or hidden.
	Backface string
}

// transformInfoForBox resolves the transform metadata for one box. parentAccum
// is the parent accumulated matrix threaded down the placed walk and viewport
// is the nearest SVG viewport at this box, so Own and World resolve
// transform-origin against the same reference box the stamp walk uses.
//
// Parent, origin, and World are populated even when the element has no static
// transform. An element can be animated by a keyframe with no static transform,
// and the host still needs its parent matrix and origin to compose the animated
// one. HasMatrix reports whether the stylesheet declares a transform; Own is the
// identity when it does not.
func transformInfoForBox(boxNode *box, parentAccum Matrix2D, viewport svgViewport) TransformInfo {
	if boxNode == nil || boxNode.style == nil {
		return TransformInfo{} //nolint:exhaustruct // no box, no metadata
	}

	sty := boxNode.style
	originX, originY := resolveTransformOriginWithBox(sty.TransformOrigin, boxNode, sty.TransformBox, viewport)

	info := TransformInfo{
		OriginX:            originX,
		OriginY:            originY,
		Box:                sty.TransformBox,
		Parent:             parentAccum,
		Own:                boxTransformAccum(boxNode, IdentityMatrix(), viewport),
		World:              boxTransformAccum(boxNode, parentAccum, viewport),
		HasMatrix:          sty.HasTransform,
		HasTransform3D:     sty.HasTransform3D,
		Perspective:        0,
		PerspectiveOriginX: 0,
		PerspectiveOriginY: 0,
		HasPerspective:     sty.HasPerspective,
		Style:              sty.TransformStyle,
		Backface:           sty.BackfaceVisibility,
	}
	if sty.HasPerspective {
		info.Perspective = sty.PerspectiveDist
		info.PerspectiveOriginX, info.PerspectiveOriginY = perspectiveCenter(boxNode)
	}

	return info
}
