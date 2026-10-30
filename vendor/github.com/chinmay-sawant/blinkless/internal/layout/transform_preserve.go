package layout

// worldTransform3DForBox returns one box's transform in canvas coordinates.
// Preserve-3d chains use these world-space matrices so each box keeps its own
// transform origin while the parent and child matrices compose.
func worldTransform3DForBox(boxNode *box, viewport svgViewport) matrix3D {
	if boxNode == nil || boxNode.style == nil {
		return identity3D()
	}

	style := boxNode.style

	local2D := IdentityMatrix()
	if style.HasTransform {
		local2D = withPercentTranslate(style, boxNode)
	}

	local3D := identity3D()
	if style.HasTransform3D {
		local3D = baked3DForBox(boxNode)
	}

	originX, originY := resolveTransformOriginWithBox(
		style.TransformOrigin, boxNode, style.TransformBox, viewport,
	)
	local := matrix3DFromAffine(local2D).mul(local3D)

	return translate3D(originX, originY, 0).mul(local).mul(translate3D(-originX, -originY, 0))
}

func matrix3DFromAffine(matrix Matrix2D) matrix3D {
	return matrix3D{
		matrix.A, matrix.C, 0, matrix.E,
		matrix.B, matrix.D, 0, matrix.F,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}
}

// stampPreserveTransformRange applies the accumulated preserve-3d projection
// to either a box's exclusive operations or its complete subtree. A flat
// boundary owns the latter because it flattens its descendants onto one plane.
func stampPreserveTransformRange(
	boxNode *box, worldChain matrix3D, parentAccum Matrix2D, ops []Op,
	covered []bool, exclusive bool, viewport svgViewport,
) {
	if boxNode == nil || boxNode.style == nil || !boxOwnsOps(boxNode) || len(ops) == 0 {
		return
	}

	start, end := boxNode.opStart, min(boxNode.opEnd, len(ops)-1)
	if start < 0 || start > end {
		return
	}

	dist, projection, affine := preserveRangePaint(boxNode, worldChain, parentAccum, viewport)

	stampPreserveRangeOps(ops, covered, start, end, exclusive, dist, projection, affine)
}

// preserveRangePaint returns the camera distance, the projective matrix, and
// the flattened affine a preserve-range stamp applies. The last two are
// alternatives: a camera projects, no camera flattens.
func preserveRangePaint(
	boxNode *box, worldChain matrix3D, parentAccum Matrix2D, viewport svgViewport,
) (float64, ProjectiveTransform, Matrix2D) {
	originX, originY := resolveTransformOriginWithBox(
		boxNode.style.TransformOrigin, boxNode, boxNode.style.TransformBox, viewport,
	)
	dist, vanishX, vanishY := perspectiveFlattenArgs(boxNode, worldChain, originX, originY)

	if dist > 0 {
		projection := projectiveTransform3DWorld(worldChain, vanishX, vanishY, dist)

		return dist, composeProjectiveAffine(projection, parentAccum), Matrix2D{} //nolint:exhaustruct // projective path
	}

	// Orthographic: no projective matrix, only the flattened affine.
	//nolint:exhaustruct // the zero projective is unused on this path
	return dist, ProjectiveTransform{}, parentAccum.Mul(matrix2DFromWorld3D(worldChain))
}

// stampPreserveRangeWindow composes one preserve-range stamp onto an explicit
// operation window. The original walk applies an ancestor's flat-boundary
// composition over the ancestor's whole range after its children stamped, so a
// subtree restamp has to re-apply the same composition over the window it
// rewrote.
func stampPreserveRangeWindow(
	boxNode *box, worldChain matrix3D, parentAccum Matrix2D, ops []Op,
	start, end int, viewport svgViewport,
) {
	if boxNode == nil || boxNode.style == nil || len(ops) == 0 {
		return
	}

	end = min(end, len(ops)-1)
	if start < 0 || start > end {
		return
	}

	dist, projection, affine := preserveRangePaint(boxNode, worldChain, parentAccum, viewport)

	stampPreserveRangeOps(ops, nil, start, end, false, dist, projection, affine)
}

// stampPreserveRangeOps stamps every op the range owns, dispatching on the
// projection mode chosen for the box.
func stampPreserveRangeOps(
	ops []Op, covered []bool, start, end int, exclusive bool, dist float64,
	projection ProjectiveTransform, affine Matrix2D,
) {
	for idx := start; idx <= end; idx++ {
		if exclusive && covered[idx] {
			continue
		}

		operation := &ops[idx]
		if operation.Kind == OpKindNoop || operation.Kind == OpUnknown {
			continue
		}

		if dist > 0 {
			stampPreserveProjectiveOp(operation, projection, exclusive)
		} else {
			stampPreserveAffineOp(operation, affine, exclusive)
		}
	}
}

// stampPreserveProjectiveOp stamps one op inside a projected preserve-3d
// range, mirroring the branch order of the original projection pass.
func stampPreserveProjectiveOp(operation *Op, projection ProjectiveTransform, exclusive bool) {
	if operation.Kind == OpImage {
		mapping := projection
		if !exclusive {
			mapping = composeProjectiveInputAffine(mapping, operation.Transform())
		}

		setPreserveImageTransform(operation, mapping)

		return
	}

	if !exclusive {
		if inner, ok := operation.ProjectiveTransformValue(); ok {
			operation.setProjectiveTransform(composeProjectiveTransforms(projection, inner))
			operation.XformSet = false

			return
		}

		if operation.XformSet {
			operation.setProjectiveTransform(composeProjectiveInputAffine(projection, operation.Transform()))
			operation.XformSet = false

			return
		}
	}

	operation.setProjectiveTransform(projection)
	operation.XformSet = false
}

// stampPreserveAffineOp stamps one op inside an unprojected preserve-3d range
// with the flattened 2D affine accumulated for this box.
func stampPreserveAffineOp(operation *Op, affine Matrix2D, exclusive bool) {
	transform := affine

	if !exclusive {
		if operation.XformSet {
			transform = transform.Mul(operation.Transform())
		}

		if inner, ok := operation.ProjectiveTransformValue(); ok {
			if operation.Kind == OpImage {
				setPreserveImageTransform(operation, composeProjectiveAffine(inner, affine))

				return
			}

			operation.setProjectiveTransform(composeProjectiveAffine(inner, affine))
			operation.XformSet = false

			return
		}
	}

	operation.setXform(transform)
	operation.XformSet = true
}

func matrix2DFromWorld3D(world matrix3D) Matrix2D {
	return Matrix2D{
		A: world[0], B: world[4], C: world[1], D: world[5], E: world[3], F: world[7],
	}
}

func projectiveTransform3DWorld(world matrix3D, vanishX, vanishY, dist float64) ProjectiveTransform {
	wCoeffX, wCoeffY, wCoeff0 := -world[8], -world[9], dist-world[11]

	return ProjectiveTransform{
		A: vanishX*wCoeffX + dist*world[0],
		B: vanishY*wCoeffX + dist*world[4],
		C: vanishX*wCoeffY + dist*world[1],
		D: vanishY*wCoeffY + dist*world[5],
		E: vanishX*wCoeff0 + dist*(world[3]-vanishX),
		F: vanishY*wCoeff0 + dist*(world[7]-vanishY),
		G: wCoeffX, H: wCoeffY, I: wCoeff0,
	}
}

func composeProjectiveInputAffine(projective ProjectiveTransform, affine Matrix2D) ProjectiveTransform {
	return composeProjectiveTransforms(projective, ProjectiveTransform{ //nolint:exhaustruct // affine lift: G, H stay zero
		A: affine.A, B: affine.B, C: affine.C, D: affine.D, E: affine.E, F: affine.F, I: 1,
	})
}

func composeProjectiveTransforms(outer, inner ProjectiveTransform) ProjectiveTransform {
	return ProjectiveTransform{
		A: outer.A*inner.A + outer.C*inner.B + outer.E*inner.G,
		B: outer.B*inner.A + outer.D*inner.B + outer.F*inner.G,
		C: outer.A*inner.C + outer.C*inner.D + outer.E*inner.H,
		D: outer.B*inner.C + outer.D*inner.D + outer.F*inner.H,
		E: outer.A*inner.E + outer.C*inner.F + outer.E*inner.I,
		F: outer.B*inner.E + outer.D*inner.F + outer.F*inner.I,
		G: outer.G*inner.A + outer.H*inner.B + outer.I*inner.G,
		H: outer.G*inner.C + outer.H*inner.D + outer.I*inner.H,
		I: outer.G*inner.E + outer.H*inner.F + outer.I*inner.I,
	}
}

// centerDivisor halves a box's width and height to reach its center.
const centerDivisor = 2

func setPreserveImageTransform(operation *Op, projective ProjectiveTransform) {
	cx, cy := operation.X+operation.W/centerDivisor, operation.Y+operation.H/centerDivisor
	x, y := projective.Apply(cx, cy)
	jacobian := projective.Jacobian(cx, cy)
	jacobian.E = x - jacobian.A*cx - jacobian.C*cy
	jacobian.F = y - jacobian.B*cx - jacobian.D*cy
	operation.setXform(jacobian)
	operation.XformSet = true
	operation.clearProjectiveTransform()
}
