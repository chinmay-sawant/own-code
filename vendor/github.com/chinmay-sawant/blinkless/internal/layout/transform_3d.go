package layout

import (
	"math"
	"strings"
)

// 3D transform core for the CSS Transforms 2 flattening subset.
//
// The paint pipeline stays 2D: every 3D list is projected onto the page plane
// at stamp time through flatten3D. No 3D scene graph exists; nested 3D keeps
// working because each level flattens in order (transform-style:flat) while
// preserve-3d composes matrices before flattening (see stampBoxTransformsRec).
// Chrome 143.0.7499.40 is the reference for all numeric choices below.
//
// Coordinate convention: row-major 4x4, column vectors, CSS handedness (+x
// right, +y down, +z toward the viewer). The facing sign is the transformed
// +z normal: positive means the front face looks at the viewer.

// matrix3D is a row-major 4x4 3D transform. The zero value is not identity;
// build one with identity3D.
type matrix3D [16]float64

// identity3D returns the 4x4 identity matrix.
func identity3D() matrix3D {
	return matrix3D{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}
}

// mul post-multiplies: acc.mul(f) applies f after acc, matching the
// left-to-right ordering of parseTransformList (M = M * Fi).
func (mat matrix3D) mul(other matrix3D) matrix3D {
	var out matrix3D

	for row := range 4 {
		for col := range 4 {
			sum := 0.0
			for k := range 4 {
				sum += mat[row*4+k] * other[k*4+col]
			}

			out[row*4+col] = sum
		}
	}

	return out
}

// rotateX3D rotates around the horizontal axis; positive degrees tip the top
// away from the viewer.
func rotateX3D(deg float64) matrix3D {
	rad := deg * math.Pi / degreesInHalfCircle
	cos, sin := math.Cos(rad), math.Sin(rad)

	return matrix3D{
		1, 0, 0, 0,
		0, cos, -sin, 0,
		0, sin, cos, 0,
		0, 0, 0, 1,
	}
}

// rotateY3D rotates around the vertical axis; positive degrees turn the right
// edge away from the viewer.
func rotateY3D(deg float64) matrix3D {
	rad := deg * math.Pi / degreesInHalfCircle
	cos, sin := math.Cos(rad), math.Sin(rad)

	return matrix3D{
		cos, 0, sin, 0,
		0, 1, 0, 0,
		-sin, 0, cos, 0,
		0, 0, 0, 1,
	}
}

// rotateZ3D rotates in the page plane; identical to the 2D RotateDeg.
func rotateZ3D(deg float64) matrix3D {
	rad := deg * math.Pi / degreesInHalfCircle
	cos, sin := math.Cos(rad), math.Sin(rad)

	return matrix3D{
		cos, -sin, 0, 0,
		sin, cos, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}
}

// rotate3DAxis rotates around an arbitrary (x, y, z) axis by Rodrigues'
// formula. A near-zero axis is rejected by the caller.
func rotate3DAxis(axisX, axisY, axisZ, deg float64) matrix3D {
	rad := deg * math.Pi / degreesInHalfCircle
	cos, sin := math.Cos(rad), math.Sin(rad)
	oneMinus := 1 - cos

	return matrix3D{
		cos + axisX*axisX*oneMinus, axisX*axisY*oneMinus - axisZ*sin, axisX*axisZ*oneMinus + axisY*sin, 0,
		axisY*axisX*oneMinus + axisZ*sin, cos + axisY*axisY*oneMinus, axisY*axisZ*oneMinus - axisX*sin, 0,
		axisZ*axisX*oneMinus - axisY*sin, axisZ*axisY*oneMinus + axisX*sin, cos + axisZ*axisZ*oneMinus, 0,
		0, 0, 0, 1,
	}
}

// translateZ3D moves the plane toward the viewer by depth points.
func translateZ3D(depth float64) matrix3D {
	return matrix3D{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, depth,
		0, 0, 0, 1,
	}
}

// scaleZ3D scales depth; it only shows through a perspective divide.
func scaleZ3D(factor float64) matrix3D {
	return matrix3D{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, factor, 0,
		0, 0, 0, 1,
	}
}

// perspective3D is the CSS perspective(d) projection matrix.
func perspective3D(d float64) matrix3D {
	return matrix3D{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		0, 0, -1 / d, 1,
	}
}

// apply3D transforms one point (px, py, pz) by mat.
func (mat matrix3D) apply3D(px, py, pz float64) (float64, float64, float64) {
	return mat[0]*px + mat[1]*py + mat[2]*pz + mat[3],
		mat[4]*px + mat[5]*py + mat[6]*pz + mat[7],
		mat[8]*px + mat[9]*py + mat[10]*pz + mat[11]
}

// flatten3D projects m onto the page plane and returns the 2D bake plus the
// facing sign (transformed +z normal; negative means the back face looks at
// the viewer). The vanishing point is (vx, vy) in the same coordinate space
// as the baked matrix. dist is the camera distance in points: dist > 0
// applies the perspective divide s = dist/(dist-z), dist <= 0 selects
// orthographic projection (what Chrome renders with no perspective).
// Degenerate input (w collapse behind the camera) clamps s to stay finite.
// perspectiveFoldEpsilon clamps the perspective divide denominator so points
// collapsing onto or behind the camera stay finite.
const perspectiveFoldEpsilon = 1e-6

func projectiveTransform3D(
	mat matrix3D, originX, originY, vanishX, vanishY, dist float64,
) ProjectiveTransform {
	if dist <= 0 {
		return ProjectiveTransform{} //nolint:exhaustruct // orthographic path: no projective matrix
	}

	// The denominator is dist-z after the 3D transform. The projected
	// numerator keeps the perspective origin relative to transform-origin.
	wCoeffX := -mat[8]
	wCoeffY := -mat[9]
	wCoeff0 := dist + mat[8]*originX + mat[9]*originY - mat[11]

	xCoeffX := vanishX*wCoeffX + dist*mat[0]
	xCoeffY := vanishX*wCoeffY + dist*mat[1]
	xCoeff0 := vanishX*wCoeff0 + dist*(mat[3]-mat[0]*originX-mat[1]*originY-vanishX+originX)

	yCoeffX := vanishY*wCoeffX + dist*mat[4]
	yCoeffY := vanishY*wCoeffY + dist*mat[5]
	yCoeff0 := vanishY*wCoeff0 + dist*(mat[7]-mat[4]*originX-mat[5]*originY-vanishY+originY)

	return ProjectiveTransform{
		A: xCoeffX, B: yCoeffX,
		C: xCoeffY, D: yCoeffY,
		E: xCoeff0, F: yCoeff0,
		G: wCoeffX, H: wCoeffY, I: wCoeff0,
	}
}

func (mat matrix3D) flatten3D(vanishX, vanishY, dist float64) (Matrix2D, float64) {
	project := func(px, py, pz float64) (float64, float64) {
		transX, transY, transZ := mat.apply3D(px, py, pz)
		scale := 1.0

		if dist > 0 {
			denom := dist - transZ
			if denom <= dist*perspectiveFoldEpsilon {
				denom = dist * perspectiveFoldEpsilon
			}

			scale = dist / denom
		}

		return vanishX + (transX-vanishX)*scale, vanishY + (transY-vanishY)*scale
	}

	// Map the unit-square corners (z = 0 plane) and read off the 2D matrix.
	ox, oy := project(0, 0, 0)
	xx, xy := project(1, 0, 0)
	yx, yy := project(0, 1, 0)

	baked := Matrix2D{A: xx - ox, B: xy - oy, C: yx - ox, D: yy - oy, E: ox, F: oy}

	// Facing is the z component of the transformed +z normal.
	facing := mat[10]

	return baked, facing
}

// parse3DFunc parses one 3D transform function into a matrix. 2D functions
// are handled by parseOneTransformFunc; unknown names return false. Lengths
// accept the translate unit set (percentages rejected: meaningless in depth).
func parse3DFunc(name, args string) (matrix3D, bool) {
	parts := splitTransformArgs(args)

	switch strings.ToLower(name) {
	case "translate3d", "scale3d":
		// The 2D arm in parseOneTransformFunc owns these. A 3D-only parse
		// would drop tx and ty off the stamped matrix.
		return matrix3D{}, false
	case "rotatex", "rotatey", "rotatez":
		return parseAxisRotate3DFunc(name, parts)
	case "rotate3d":
		return parseRotate3DAxisFunc(parts)
	case "translatez":
		return parseTranslateZFunc(parts)
	case "scalez":
		return parseScaleZFunc(parts)
	case "perspective":
		return parsePerspectiveFunc(parts)
	case "matrix3d":
		return parseMatrix3DFunc(parts)
	default:
		return matrix3D{}, false
	}
}

// parseAxisRotate3DFunc parses rotateX/Y/Z(angle); name arrives lowercased
// from parse3DFunc.
func parseAxisRotate3DFunc(name string, parts []string) (matrix3D, bool) {
	if len(parts) != 1 {
		return matrix3D{}, false
	}

	deg, ok := parseAngleDeg(parts[0])
	if !ok {
		return matrix3D{}, false
	}

	switch name {
	case "rotatex":
		return rotateX3D(deg), true
	case "rotatey":
		return rotateY3D(deg), true
	default:
		return rotateZ3D(deg), true
	}
}

// rotate3dAxisParts is the rotate3d(x, y, z, angle) arity.
const rotate3dAxisParts = 4

// axisEpsilon rejects a near-zero rotate3d axis.
const axisEpsilon = 1e-9

// parseRotate3DAxisFunc parses rotate3d(x, y, z, angle) with a normalized
// axis via Rodrigues' formula.
func parseRotate3DAxisFunc(parts []string) (matrix3D, bool) {
	if len(parts) != rotate3dAxisParts {
		return matrix3D{}, false
	}

	nums := make([]float64, rotate3dAxisParts)

	for idx, part := range parts[:3] {
		num, ok := parseUnitless(part)
		if !ok {
			return matrix3D{}, false
		}

		nums[idx] = num
	}

	deg, ok := parseAngleDeg(parts[3])
	if !ok {
		return matrix3D{}, false
	}

	nums[3] = deg

	length := math.Sqrt(nums[0]*nums[0] + nums[1]*nums[1] + nums[2]*nums[2])
	if length < axisEpsilon {
		return matrix3D{}, false
	}

	return rotate3DAxis(nums[0]/length, nums[1]/length, nums[2]/length, nums[3]), true
}

// parseTranslate3DFunc parses translate3d(tx, ty, tz). tx and ty follow the
// 2D translate path, including percentages. A percentage tz is rejected.
func parseTranslate3DFunc(parts []string, fontSize float64) (Matrix2D, float64, float64, matrix3D, bool) {
	if len(parts) != maxThreeValueArgs {
		return Matrix2D{}, math.NaN(), math.NaN(), matrix3D{}, false //nolint:exhaustruct // zero matrix is unused on failure
	}

	plane, xPct, yPct, ok := parseTranslateFunc("translate", parts[:2], fontSize)
	if !ok {
		return Matrix2D{}, math.NaN(), math.NaN(), matrix3D{}, false //nolint:exhaustruct // zero matrix is unused on failure
	}

	depth, _, isPercent, depthOK := parseTransformLength(parts[2], fontSize)
	if !depthOK || isPercent {
		return Matrix2D{}, math.NaN(), math.NaN(), matrix3D{}, false //nolint:exhaustruct // zero matrix is unused on failure
	}

	return plane, xPct, yPct, translateZ3D(depth), true
}

// parseScale3DFunc parses scale3d(sx, sy, sz). One number does not apply.
func parseScale3DFunc(parts []string) (Matrix2D, matrix3D, bool) {
	if len(parts) != maxThreeValueArgs {
		return Matrix2D{}, matrix3D{}, false //nolint:exhaustruct // zero matrix is unused on failure
	}

	plane, ok := parseScaleFunc("scale", parts[:2])
	if !ok {
		return Matrix2D{}, matrix3D{}, false //nolint:exhaustruct // zero matrix is unused on failure
	}

	depth, depthOK := parseUnitless(parts[2])
	if !depthOK {
		return Matrix2D{}, matrix3D{}, false //nolint:exhaustruct // zero matrix is unused on failure
	}

	return plane, scaleZ3D(depth), true
}

// appendTransform3D post-multiplies one side matrix onto the style.
func appendTransform3D(style *ResolvedStyle, extra matrix3D) {
	base := identity3D()
	if style.HasTransform3D {
		base = matrix3D(style.Transform3D)
	}

	next := base.mul(extra)
	style.Transform3D = next
	style.HasTransform3D = true
}

// parseTranslateZFunc parses translateZ(length); percentages are meaningless
// in depth. parseTransformLength returns (pt, pct, isPct, ok); the pct slot
// keeps its conventional name.
func parseTranslateZFunc(parts []string) (matrix3D, bool) {
	if len(parts) != 1 {
		return matrix3D{}, false
	}

	pt, _, isPct, ok := parseTransformLength(parts[0], 0)
	if !ok || isPct {
		return matrix3D{}, false
	}

	return translateZ3D(pt), true
}

// parseScaleZFunc parses scaleZ(number).
func parseScaleZFunc(parts []string) (matrix3D, bool) {
	if len(parts) != 1 {
		return matrix3D{}, false
	}

	num, ok := parseUnitless(parts[0])
	if !ok {
		return matrix3D{}, false
	}

	return scaleZ3D(num), true
}

// parsePerspectiveFunc parses perspective(length) with a positive distance.
func parsePerspectiveFunc(parts []string) (matrix3D, bool) {
	if len(parts) != 1 {
		return matrix3D{}, false
	}

	pt, _, isPct, ok := parseTransformLength(parts[0], 0)
	if !ok || isPct || pt <= 0 {
		return matrix3D{}, false
	}

	return perspective3D(pt), true
}

// matrix3dArgCount is the matrix3d() arity.
const matrix3dArgCount = 16

// parseMatrix3DFunc parses matrix3d() transposing CSS column-major order
// into row-major storage.
func parseMatrix3DFunc(parts []string) (matrix3D, bool) {
	if len(parts) != matrix3dArgCount {
		return matrix3D{}, false
	}

	var out matrix3D

	for idx, part := range parts {
		num, ok := parseUnitless(part)
		if !ok {
			return matrix3D{}, false
		}

		out[idx] = num
	}

	return matrix3D{
		out[0], out[4], out[8], out[12],
		out[1], out[5], out[9], out[13],
		out[2], out[6], out[10], out[14],
		out[3], out[7], out[11], out[15],
	}, true
}

// perspectiveCenter returns the vanishing point for the perspective set on
// perspBox: its border-box origin shifted by perspective-origin. The default
// (no perspective-origin declaration) is the border-box center per CSS
// Transforms 2. propagatePerspective calls this when it fills a descendant.
// Reference: Chrome 143.0.7499.40.
func perspectiveCenter(perspBox *box) (float64, float64) {
	if perspBox == nil {
		return 0, 0
	}

	const (
		halfDivisor    = 2
		percentDivisor = 100
	)

	if perspBox.style == nil || !perspBox.style.PerspectiveOriginSet {
		return perspBox.x + perspBox.w/halfDivisor, perspBox.y + perspBox.height/halfDivisor
	}

	originX := perspBox.style.PerspectiveOriginX

	if perspBox.style.PerspectiveOriginXPct {
		originX = perspBox.style.PerspectiveOriginX / percentDivisor * perspBox.w
	}

	originY := perspBox.style.PerspectiveOriginY

	if perspBox.style.PerspectiveOriginYPct {
		originY = perspBox.style.PerspectiveOriginY / percentDivisor * perspBox.height
	}

	return perspBox.x + originX, perspBox.y + originY
}
