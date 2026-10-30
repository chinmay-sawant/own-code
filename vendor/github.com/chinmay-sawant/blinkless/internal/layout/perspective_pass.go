package layout

// Perspective pre-pass: inherit the nearest ancestor perspective property
// into 3D-bearing descendants before stamp time.
//
// CSS Transforms 2 gives the perspective property to children, not to the
// element itself: a child with a 3D transform flattens through the nearest
// ancestor with perspective set, using that ancestor's perspective-origin as
// the vanishing point. This pass walks the box tree via children, tracks the
// nearest ancestor with HasPerspective, and fills each HasTransform3D
// descendant's perspDist/hasPerspDist plus perspCX/perspCY via
// perspectiveCenter (owned by the perspective-origin workstream).
// boxTransformAccum reads the filled fields when projecting; preserveComposed
// boxes are still filled here and skipped there. Reference: Chrome
// 143.0.7499.40.
func propagatePerspective(rootBox *box) {
	walkPerspective(rootBox, nil)
}

// propagatePerspectiveFrom re-runs the pre-pass for one subtree. seed is the
// nearest ancestor outside the subtree that carries a perspective, or nil.
// Every box in the subtree has its computed camera fields cleared first, so an
// override to none restores the camera the seed carries instead of keeping the
// distance a previous stamp left on the box.
func propagatePerspectiveFrom(rootBox, seed *box) {
	clearPerspectiveFields(rootBox)
	walkPerspective(rootBox, seed)
}

// clearPerspectiveFields drops the camera state the pre-pass computed on one
// subtree. The fields are derived from the styles above the box, so the next
// fill must start from nothing.
func clearPerspectiveFields(boxNode *box) {
	if boxNode == nil {
		return
	}

	boxNode.perspDist = 0
	boxNode.hasPerspDist = false
	boxNode.perspCX = 0
	boxNode.perspCY = 0

	for _, child := range boxNode.children {
		clearPerspectiveFields(child)
	}
}

// walkPerspective fills each 3D-bearing descendant with the nearest ancestor
// camera.
func walkPerspective(rootBox, seed *box) {
	if rootBox == nil {
		return
	}

	var walk func(boxNode *box, ancestor *box)
	walk = func(boxNode *box, ancestor *box) {
		if boxNode == nil {
			return
		}

		if boxNode.style != nil && boxNode.style.HasTransform3D && ancestor != nil {
			boxNode.perspDist = ancestor.style.PerspectiveDist
			boxNode.hasPerspDist = true

			centerX, centerY := perspectiveCenter(ancestor)
			boxNode.perspCX, boxNode.perspCY = centerX, centerY
		}

		nextAncestor := ancestor
		if boxNode.style != nil && boxNode.style.HasPerspective {
			nextAncestor = boxNode
		}

		for _, child := range boxNode.children {
			walk(child, nextAncestor)
		}
	}

	walk(rootBox, seed)
}

// perspectiveFlattenArgs selects the flatten3D camera for one 3D box: the
// inherited ancestor perspective when the pre-pass filled it, else the
// perspective() function's own distance with the transform origin as the
// vanishing point, else orthographic (dist <= 0, Chrome with no
// perspective). Reference: Chrome 143.0.7499.40.
func perspectiveFlattenArgs(boxNode *box, chain matrix3D, originX, originY float64) (float64, float64, float64) {
	if boxNode != nil && boxNode.hasPerspDist {
		return boxNode.perspDist, boxNode.perspCX, boxNode.perspCY
	}

	if dist, ok := selfPerspectiveDist(chain); ok {
		return dist, originX, originY
	}

	return -1, 0, 0
}

// selfPerspectiveDist extracts the perspective() function distance from a
// composed 3D matrix. Only perspective() and matrix3d with a perspective row
// produce a non-trivial w row; rotate/translate/scale keep [0 0 0 1]. A
// negative w-row entry carries -1/d (exact for a lone perspective() or a
// trailing one, scaled by cos for a leading one composed with a rotation),
// so -1/entry recovers a usable camera distance that still foreshortens
// differently from orthographic. Reference: Chrome 143.0.7499.40.
func selfPerspectiveDist(chain matrix3D) (float64, bool) {
	const eps = 1e-12
	if chain[14] < -eps {
		return -1 / chain[14], true
	}

	return 0, false
}
