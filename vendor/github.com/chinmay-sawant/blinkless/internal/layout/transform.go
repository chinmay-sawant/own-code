package layout

import (
	"math"
	"strconv"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/css"
)

const (
	transformFuncTranslatex = "translatex"
	transformFuncScalex     = "scalex"
	transformFuncSkewx      = "skewx"
)

const (
	degreesInHalfCircle   = 180
	transformOriginMiddle = 50
	matrixFuncArgCount    = 6
	matrixTranslateStart  = 4
	gradToDegFactor       = 0.9
	fullTurnDegrees       = 360
	maxTwoValueArgs       = 2
	maxThreeValueArgs     = 3
)

// Matrix2D is a CSS/SVG-style 2D affine transform:
//
//	x' = A*x + C*y + E
//	y' = B*x + D*y + F
type Matrix2D struct {
	A, B, C, D, E, F float64
}

// ProjectiveTransform maps canvas points through a 3D perspective plane.
// A point maps to ((A*x+C*y+E)/w, (B*x+D*y+F)/w), where w=G*x+H*y+I.
type ProjectiveTransform struct {
	A, B, C, D, E, F, G, H, I float64
}

// Apply maps one canvas point through the projective transform.
func (m ProjectiveTransform) Apply(pointX, pointY float64) (float64, float64) {
	denom := m.G*pointX + m.H*pointY + m.I
	if denom == 0 {
		return math.Copysign(math.MaxFloat64, m.A*pointX+m.C*pointY+m.E),
			math.Copysign(math.MaxFloat64, m.B*pointX+m.D*pointY+m.F)
	}

	return (m.A*pointX + m.C*pointY + m.E) / denom, (m.B*pointX + m.D*pointY + m.F) / denom
}

// Jacobian returns the local affine transform at one canvas point.
func (m ProjectiveTransform) Jacobian(pointX, pointY float64) Matrix2D {
	denom := m.G*pointX + m.H*pointY + m.I
	if denom == 0 {
		return Matrix2D{} //nolint:exhaustruct // degenerate point: no finite local transform
	}

	numX := m.A*pointX + m.C*pointY + m.E
	numY := m.B*pointX + m.D*pointY + m.F
	denomSquared := denom * denom

	return Matrix2D{ //nolint:exhaustruct // translation terms stay zero
		A: (m.A*denom - numX*m.G) / denomSquared,
		B: (m.B*denom - numY*m.G) / denomSquared,
		C: (m.C*denom - numX*m.H) / denomSquared,
		D: (m.D*denom - numY*m.H) / denomSquared,
	}
}

// IdentityMatrix returns the identity transform.
func IdentityMatrix() Matrix2D {
	return Matrix2D{A: 1, D: 1} //nolint:exhaustruct // intentional zero fields
}

// IsIdentity reports whether m is the identity (within a small epsilon).
func (m Matrix2D) IsIdentity() bool {
	const eps = 1e-9

	return math.Abs(m.A-1) < eps && math.Abs(m.D-1) < eps &&
		math.Abs(m.B) < eps && math.Abs(m.C) < eps &&
		math.Abs(m.E) < eps && math.Abs(m.F) < eps
}

// Mul returns m * n (apply n first, then m) for column vectors.
func (m Matrix2D) Mul(node Matrix2D) Matrix2D {
	return Matrix2D{
		A: m.A*node.A + m.C*node.B,
		B: m.B*node.A + m.D*node.B,
		C: m.A*node.C + m.C*node.D,
		D: m.B*node.C + m.D*node.D,
		E: m.A*node.E + m.C*node.F + m.E,
		F: m.B*node.E + m.D*node.F + m.F,
	}
}

// Translate returns a pure translation matrix.
func Translate(tx, ty float64) Matrix2D {
	return Matrix2D{A: 1, D: 1, E: tx, F: ty} //nolint:exhaustruct // intentional zero fields
}

// Scale returns a pure scale matrix about the origin.
func Scale(sx, sy float64) Matrix2D {
	return Matrix2D{A: sx, D: sy} //nolint:exhaustruct // intentional zero fields
}

// RotateDeg returns a rotation by deg degrees about the origin (CSS y-down).
func RotateDeg(deg float64) Matrix2D {
	rad := deg * math.Pi / degreesInHalfCircle
	c, s := math.Cos(rad), math.Sin(rad)

	return Matrix2D{A: c, B: s, C: -s, D: c} //nolint:exhaustruct // intentional zero fields
}

// SkewXDeg returns a skewX matrix (CSS degrees).
func SkewXDeg(deg float64) Matrix2D {
	t := math.Tan(deg * math.Pi / degreesInHalfCircle)

	return Matrix2D{A: 1, D: 1, C: t} //nolint:exhaustruct // intentional zero fields
}

// SkewYDeg returns a skewY matrix (CSS degrees).
func SkewYDeg(deg float64) Matrix2D {
	t := math.Tan(deg * math.Pi / degreesInHalfCircle)

	return Matrix2D{A: 1, D: 1, B: t} //nolint:exhaustruct // intentional zero fields
}

// BakeOrigin returns T(ox,oy) * m * T(-ox,-oy).
func BakeOrigin(m Matrix2D, ox, oy float64) Matrix2D {
	return Translate(ox, oy).Mul(m).Mul(Translate(-ox, -oy))
}

// Apply maps (x,y) through m.
func (m Matrix2D) Apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

// transformOriginSpec stores unresolved transform-origin components.
// Percentages are 0–100 against the border box; lengths are absolute pt.
type transformOriginSpec struct {
	X, Y     float64
	XPercent bool
	YPercent bool
}

func defaultTransformOrigin() transformOriginSpec {
	return transformOriginSpec{X: transformOriginMiddle, Y: transformOriginMiddle, XPercent: true, YPercent: true}
}

func resolveTransformOrigin(spec transformOriginSpec, boxNode *box) (float64, float64) {
	return resolveBorderBoxOrigin(spec, boxNode)
}

// resolveTransformOriginWithBox resolves transform-origin against the
// transform-box reference box. Only content-box differs from the border box
// for CSS layout boxes: percentages resolve against the content size and
// lengths offset from the content origin (border plus padding inset).
// fill-box, stroke-box, and border-box all alias the border box for CSS
// boxes (the object bounding box of a CSS box is its border box). view-box
// resolves against the nearest SVG viewport carried by the stamp walk and
// falls back to the border box when the box has no SVG ancestor (a plain
// HTML box). Reference: Chrome 143.0.7499.40.
func resolveTransformOriginWithBox(
	spec transformOriginSpec, boxNode *box, transformBox string, viewport svgViewport,
) (float64, float64) {
	if boxNode == nil {
		return 0, 0
	}

	if transformBox == "content-box" {
		return resolveContentBoxOrigin(spec, boxNode)
	}

	if transformBox == "view-box" && viewport.set {
		return resolveViewportOrigin(spec, viewport)
	}

	return resolveTransformOrigin(spec, boxNode)
}

// resolveViewportOrigin resolves transform-origin against the nearest SVG
// viewport rectangle: percentages scale the viewport size, lengths offset
// from the viewport origin.
func resolveViewportOrigin(spec transformOriginSpec, viewport svgViewport) (float64, float64) {
	var originX, originY float64

	if spec.XPercent {
		originX = viewport.x + viewport.w*spec.X/oneHundred
	} else {
		originX = viewport.x + spec.X
	}

	if spec.YPercent {
		originY = viewport.y + viewport.h*spec.Y/oneHundred
	} else {
		originY = viewport.y + spec.Y
	}

	return originX, originY
}

// svgViewport carries the nearest SVG viewport rectangle (page-space pt)
// down the stamp walk so view-box origins resolve against it.
type svgViewport struct {
	x, y, w, h float64
	set        bool
}

// viewportOfBox reports the SVG viewport established by boxNode when the box
// wraps an <svg> element: the viewport origin sits at the border-box origin
// and spans the border-box size.
func viewportOfBox(boxNode *box) (svgViewport, bool) {
	if boxNode == nil || boxNode.node == nil || boxNode.node.Name != cssTagSVG {
		var none svgViewport

		return none, false
	}

	return svgViewport{x: boxNode.x, y: boxNode.y, w: boxNode.w, h: boxNode.height, set: true}, true
}

// resolveBorderBoxOrigin resolves transform-origin against the border box.
// It is the shared base for resolveTransformOrigin and
// resolveTransformOriginWithBox so neither wrapper recurses into the other.
func resolveBorderBoxOrigin(spec transformOriginSpec, boxNode *box) (float64, float64) {
	if boxNode == nil {
		return 0, 0
	}

	var originX, originY float64

	if spec.XPercent {
		originX = boxNode.x + boxNode.w*spec.X/oneHundred
	} else {
		originX = boxNode.x + spec.X
	}

	if spec.YPercent {
		originY = boxNode.y + boxNode.height*spec.Y/oneHundred
	} else {
		originY = boxNode.y + spec.Y
	}

	return originX, originY
}

// resolveContentBoxOrigin resolves transform-origin against the content box.
// Border and padding come from used style values (unscaled pt); the box
// geometry is scaled, so the two match at scale 1 (all layout tests) and
// approximate under zoom. Threading engine scale through stampBoxTransforms
// is future work.
func resolveContentBoxOrigin(spec transformOriginSpec, boxNode *box) (float64, float64) {
	var padL, padR, padT, padB, borderL, borderR, borderT, borderB float64

	if sty := boxNode.style; sty != nil {
		padL, padR, padT, padB = sty.PaddingLeft, sty.PaddingRight, sty.PaddingTop, sty.PaddingBottom
		borderL = borderPaint(sty.BorderLeft)
		borderR = borderPaint(sty.BorderRight)
		borderT = borderPaint(sty.BorderTop)
		borderB = borderPaint(sty.BorderBottom)
	}

	contentX := boxNode.x + borderL + padL
	contentY := boxNode.y + borderT + padT
	contentW := boxNode.w - borderL - borderR - padL - padR
	contentH := boxNode.height - borderT - borderB - padT - padB

	if contentW < 0 {
		contentW = 0
	}

	if contentH < 0 {
		contentH = 0
	}

	var originX, originY float64

	if spec.XPercent {
		originX = contentX + contentW*spec.X/oneHundred
	} else {
		originX = contentX + spec.X
	}

	if spec.YPercent {
		originY = contentY + contentH*spec.Y/oneHundred
	} else {
		originY = contentY + spec.Y
	}

	return originX, originY
}

// parseTransformList parses a CSS transform function list into a single matrix.
// Returns ok=false for unrecognized input (caller keeps prior/initial).
// "none" yields identity with ok=true and has=false.
// Percentage translates are deferred (NaN = none) and resolved at stamp time
// against the border box, matching the `translate:` longhand path.
// mergePercentAccum folds one deferred translate percent into its accumulator.
// NaN means "none", so a NaN delta leaves the accumulator unchanged and a NaN
// accumulator adopts the first real delta.
func mergePercentAccum(accum, delta float64) float64 {
	if math.IsNaN(delta) {
		return accum
	}

	if math.IsNaN(accum) {
		return delta
	}

	return accum + delta
}

func parseTransformList(value string, fontSize float64) (Matrix2D, float64, float64, bool, bool) {
	matrix, xPct, yPct, has, ok, _, _ := parseTransformList3D(value, fontSize)

	return matrix, xPct, yPct, has, ok
}

// parseTransformList3D parses a transform list into its 2D bake plus the
// accumulated 3D side matrix. 3D functions contribute identity to the 2D bake
// and compose onto accum3D; has3D reports whether any 3D function parsed.
func parseTransformList3D(value string, fontSize float64) (Matrix2D, float64, float64, bool, bool, matrix3D, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return IdentityMatrix(), math.NaN(), math.NaN(), false, false, matrix3D{}, false
	}

	if strings.EqualFold(value, cssDisplayNone) {
		return IdentityMatrix(), math.NaN(), math.NaN(), false, true, matrix3D{}, false
	}

	matrix := IdentityMatrix()
	accum3D := identity3D()
	has3D := false
	has := false
	accXPct, accYPct := math.NaN(), math.NaN()
	rest := value

	for {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			break
		}

		name, args, next, pok := splitTransformFunc(rest)
		if !pok {
			return IdentityMatrix(), math.NaN(), math.NaN(), false, false, matrix3D{}, false
		}

		funcMatrix, xPctVal, yPctVal, func3D, funcHas3D, funcOK := parseOneTransformFunc(name, args, fontSize)
		if !funcOK {
			return IdentityMatrix(), math.NaN(), math.NaN(), false, false, matrix3D{}, false
		}

		// Left-to-right post-multiply: M = M * Fi
		matrix = matrix.Mul(funcMatrix)

		if funcHas3D {
			accum3D = accum3D.mul(func3D)
			has3D = true
		}

		accXPct = mergePercentAccum(accXPct, xPctVal)
		accYPct = mergePercentAccum(accYPct, yPctVal)
		has = true
		rest = next
	}

	return matrix, accXPct, accYPct, has, true, accum3D, has3D
}

func splitTransformFunc(s string) (string, string, string, bool) {
	text := strings.TrimSpace(s)
	idx := scanTransformFuncName(text)

	if idx == 0 {
		return "", "", "", false
	}

	name := strings.ToLower(text[:idx])

	body := strings.TrimSpace(text[idx:])
	if len(body) == 0 || body[0] != '(' {
		return "", "", "", false
	}

	args, rest, ok := scanTransformParens(body)

	return name, args, rest, ok
}

// scanTransformFuncName returns the length of the leading identifier of a
// transform function name (letters, digits, and hyphens: rotate3d and
// matrix3d carry digits).
func scanTransformFuncName(text string) int {
	idx := 0

	for idx < len(text) && ((text[idx] >= 'a' && text[idx] <= 'z') ||
		(text[idx] >= 'A' && text[idx] <= 'Z') || text[idx] == '-' ||
		(text[idx] >= '0' && text[idx] <= '9')) {
		idx++
	}

	return idx
}

// scanTransformParens extracts the content of the top-level (...) group from
// text (which must start with '(') and returns it plus the trimmed remainder.
func scanTransformParens(text string) (string, string, bool) {
	depth := 0

	for jdx := range len(text) {
		switch text[jdx] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[1:jdx]), strings.TrimSpace(text[jdx+1:]), true
			}
		}
	}

	return "", "", false
}

func parseOneTransformFunc(name, args string, fontSize float64) (Matrix2D, float64, float64, matrix3D, bool, bool) {
	parts := splitTransformArgs(args)

	switch name {
	case "matrix":
		m, ok := parseMatrixFunc(parts)

		return m, math.NaN(), math.NaN(), matrix3D{}, false, ok
	case "translate", transformFuncTranslatex, "translatey":
		m, xPct, yPct, ok := parseTranslateFunc(name, parts, fontSize)

		return m, xPct, yPct, matrix3D{}, false, ok
	case "translate3d":
		m, xPct, yPct, side, ok := parseTranslate3DFunc(parts, fontSize)

		return m, xPct, yPct, side, ok, ok
	case "scale", transformFuncScalex, "scaley":
		m, ok := parseScaleFunc(name, parts)

		return m, math.NaN(), math.NaN(), matrix3D{}, false, ok
	case "scale3d":
		m, side, ok := parseScale3DFunc(parts)

		return m, math.NaN(), math.NaN(), side, ok, ok
	case "rotate":
		m, ok := parseRotateFunc(parts)

		return m, math.NaN(), math.NaN(), matrix3D{}, false, ok
	case "skew", transformFuncSkewx, "skewy":
		m, ok := parseSkewFunc(name, parts)

		return m, math.NaN(), math.NaN(), matrix3D{}, false, ok
	default:
		// 3D functions (rotateX/Y/Z, rotate3d, translateZ, scaleZ,
		// perspective(), matrix3d) contribute identity to the 2D bake and
		// record their matrix on the 3D side channel; the flattening
		// consumers in boxTransformAccum project them at stamp time.
		if m3, ok := parse3DFunc(name, args); ok {
			return IdentityMatrix(), math.NaN(), math.NaN(), m3, true, true
		}

		return Matrix2D{}, math.NaN(), math.NaN(), matrix3D{}, false, false //nolint:exhaustruct // intentional zero fields
	}
}

// parseMatrixFunc parses matrix(a,b,c,d,e,f) coefficients.
func parseMatrixFunc(parts []string) (Matrix2D, bool) {
	if len(parts) != matrixFuncArgCount {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	vals := make([]float64, matrixFuncArgCount)

	for idx, p := range parts {
		// CSS matrix() takes six <number>s (user units ≈ px at 96dpi).
		val, ok := parseUnitless(p)
		if !ok {
			return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
		}

		if idx >= matrixTranslateStart {
			// e,f: translate in px → pt for our canvas.
			vals[idx] = pxToPt(val)
		} else {
			vals[idx] = val
		}
	}
	// CSS matrix(a,b,c,d,e,f): x'=ax+cy+e, y'=bx+dy+f
	return Matrix2D{A: vals[0], B: vals[1], C: vals[2], D: vals[3], E: vals[4], F: vals[5]}, true
}

// parseTranslateFunc parses translate(tx[, ty]) and its translatex/translatey
// one-axis variants. Percentage components are deferred (NaN = none).
func parseTranslateFunc(name string, parts []string, fontSize float64) (Matrix2D, float64, float64, bool) {
	if name == transformFuncTranslatex || name == "translatey" {
		return parseSingleAxisTranslate(name, parts, fontSize)
	}

	return parseTwoArgTranslate(parts, fontSize)
}

// parseSingleAxisTranslate parses translatex(tx) / translatey(ty).
func parseSingleAxisTranslate(name string, parts []string, fontSize float64) (Matrix2D, float64, float64, bool) {
	if len(parts) != 1 {
		return Matrix2D{}, math.NaN(), math.NaN(), false //nolint:exhaustruct // intentional zero fields
	}

	axisLen, pctVal, isPct, isOK := parseTransformLength(parts[0], fontSize)
	if !isOK {
		return Matrix2D{}, math.NaN(), math.NaN(), false //nolint:exhaustruct // intentional zero fields
	}

	xPct, yPct := math.NaN(), math.NaN()

	if isPct {
		axisLen = 0

		if name == transformFuncTranslatex {
			xPct = pctVal
		} else {
			yPct = pctVal
		}
	}

	if name == transformFuncTranslatex {
		return Translate(axisLen, 0), xPct, yPct, true
	}

	return Translate(0, axisLen), xPct, yPct, true
}

// parseTwoArgTranslate parses translate(tx[, ty]).
func parseTwoArgTranslate(parts []string, fontSize float64) (Matrix2D, float64, float64, bool) {
	if len(parts) < 1 || len(parts) > maxTwoValueArgs {
		return Matrix2D{}, math.NaN(), math.NaN(), false //nolint:exhaustruct // intentional zero fields
	}

	xLen, xPctVal, xIsPct, isOK := parseTransformLength(parts[0], fontSize)
	if !isOK {
		return Matrix2D{}, math.NaN(), math.NaN(), false //nolint:exhaustruct // intentional zero fields
	}

	xPct, yPct := math.NaN(), math.NaN()

	if xIsPct {
		xPct = xPctVal
		xLen = 0
	}

	yLen := 0.0

	if len(parts) == maxTwoValueArgs {
		yLenTmp, yPctVal, yIsPct, ok2 := parseTransformLength(parts[1], fontSize)
		if !ok2 {
			return Matrix2D{}, math.NaN(), math.NaN(), false //nolint:exhaustruct // intentional zero fields
		}

		if yIsPct {
			yPct = yPctVal
			yLenTmp = 0
		}

		yLen = yLenTmp
	}

	return Translate(xLen, yLen), xPct, yPct, true
}

// parseScaleFunc parses scale(sx[, sy]) and its scalex/scaley one-axis
// variants.
func parseScaleFunc(name string, parts []string) (Matrix2D, bool) {
	if name == transformFuncScalex || name == "scaley" {
		return parseSingleAxisScale(name, parts)
	}

	return parseTwoArgScale(parts)
}

// parseSingleAxisScale parses scalex(sx) / scaley(sy).
func parseSingleAxisScale(name string, parts []string) (Matrix2D, bool) {
	if len(parts) != 1 {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	axisScale, isOK := parseUnitless(parts[0])
	if !isOK {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	if name == transformFuncScalex {
		return Scale(axisScale, 1), true
	}

	return Scale(1, axisScale), true
}

// parseTwoArgScale parses scale(sx[, sy]).
func parseTwoArgScale(parts []string) (Matrix2D, bool) {
	if len(parts) < 1 || len(parts) > maxTwoValueArgs {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	xScale, isOK := parseUnitless(parts[0])
	if !isOK {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	yScale := xScale
	if len(parts) == maxTwoValueArgs {
		yScale, isOK = parseUnitless(parts[1])
		if !isOK {
			return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
		}
	}

	return Scale(xScale, yScale), true
}

// parseRotateFunc parses rotate(deg).
func parseRotateFunc(parts []string) (Matrix2D, bool) {
	if len(parts) != 1 {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	deg, ok := parseAngleDeg(parts[0])
	if !ok {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	return RotateDeg(deg), true
}

// parseSkewFunc parses skew(ax[, ay]) and its skewx/skewy one-axis variants.
func parseSkewFunc(name string, parts []string) (Matrix2D, bool) {
	if name == transformFuncSkewx || name == "skewy" {
		return parseSingleAxisSkew(name, parts)
	}

	return parseTwoArgSkew(parts)
}

// parseSingleAxisSkew parses skewx(deg) / skewy(deg).
func parseSingleAxisSkew(name string, parts []string) (Matrix2D, bool) {
	if len(parts) != 1 {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	deg, isOK := parseAngleDeg(parts[0])
	if !isOK {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	if name == transformFuncSkewx {
		return SkewXDeg(deg), true
	}

	return SkewYDeg(deg), true
}

// parseTwoArgSkew parses skew(ax[, ay]).
func parseTwoArgSkew(parts []string) (Matrix2D, bool) {
	if len(parts) < 1 || len(parts) > maxTwoValueArgs {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	xDeg, isOK := parseAngleDeg(parts[0])
	if !isOK {
		return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
	}

	yDeg := 0.0
	if len(parts) == maxTwoValueArgs {
		yDeg, isOK = parseAngleDeg(parts[1])
		if !isOK {
			return Matrix2D{}, false //nolint:exhaustruct // intentional zero fields
		}
	}

	return SkewXDeg(xDeg).Mul(SkewYDeg(yDeg)), true
}

func splitTransformArgs(args string) []string {
	if strings.TrimSpace(args) == "" {
		return nil
	}

	// Most transform functions take at most 4 args (translate/scale/rotate/
	// skew/matrix6). The stack array avoids a heap slice per function; a 5th
	// append grows onto the heap exactly like the old nil-slice append did.
	var buf [4]string
	parts := buf[:0]

	start := 0
	depth := 0

	for idx := range len(args) {
		switch args[idx] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(args[start:idx]))
				start = idx + 1
			}
		}
	}

	parts = append(parts, strings.TrimSpace(args[start:]))

	return parts
}

func parseUnitless(s string) (float64, bool) {
	s = strings.TrimSpace(s)

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}

	return f, true
}

func parseAngleDeg(cssSheet string) (float64, bool) {
	cssSheet = strings.TrimSpace(strings.ToLower(cssSheet))
	if cssSheet == "" {
		return 0, false
	}

	switch {
	case strings.HasSuffix(cssSheet, "deg"):
		v, err := strconv.ParseFloat(strings.TrimSpace(cssSheet[:len(cssSheet)-3]), 64)

		return v, err == nil
	case strings.HasSuffix(cssSheet, "rad"):
		v, err := strconv.ParseFloat(strings.TrimSpace(cssSheet[:len(cssSheet)-3]), 64)
		if err != nil {
			return 0, false
		}

		return v * degreesInHalfCircle / math.Pi, true
	case strings.HasSuffix(cssSheet, "grad"):
		v, err := strconv.ParseFloat(strings.TrimSpace(cssSheet[:len(cssSheet)-4]), 64)
		if err != nil {
			return 0, false
		}

		return v * gradToDegFactor, true
	case strings.HasSuffix(cssSheet, "turn"):
		v, err := strconv.ParseFloat(strings.TrimSpace(cssSheet[:len(cssSheet)-4]), 64)
		if err != nil {
			return 0, false
		}

		return v * fullTurnDegrees, true
	default:
		// Unitless angles are invalid in modern CSS; accept bare numbers as deg
		// for authoring convenience in fixtures.
		v, err := strconv.ParseFloat(cssSheet, 64)

		return v, err == nil
	}
}

// parseTransformLength parses a translate length (px/pt/em/%).
// For percentages it returns pct=value and isPct=true so callers can defer
// resolution against the border box at layout time (spec: % of border-box).
// Absolute lengths return pt and isPct=false. em uses font-size.
func parseTransformLength(cssS string, fontSize float64) (float64, float64, bool, bool) {
	cssS = strings.TrimSpace(cssS)
	if cssS == "0" {
		return 0, 0, false, true
	}

	val, unit, ok := css.ParseLength(cssS)
	if !ok {
		// bare number = px per CSS Transforms for matrix e/f; for translate too historically
		if f, err := strconv.ParseFloat(cssS, 64); err == nil {
			return pxToPt(f), 0, false, true
		}

		return 0, 0, false, false
	}

	if unit == "%" {
		return 0, val, true, true
	}

	if unit == "rem" {
		return pxToPt(16) * val, 0, false, true //nolint:mnd // cssPxRoot 16 inlined
	}

	if pt, ok := lengthToPt(val, unit, fontSize); ok {
		return pt, 0, false, true
	}

	return 0, 0, false, false
}

// parseTransformOrigin parses CSS transform-origin (1–3 values; z ignored).
func parseTransformOrigin(value string, fontSize float64) (transformOriginSpec, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return transformOriginSpec{}, false //nolint:exhaustruct // intentional zero fields
	}

	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > maxThreeValueArgs {
		return transformOriginSpec{}, false //nolint:exhaustruct // intentional zero fields
	}

	spec := defaultTransformOrigin()

	switch len(parts) {
	case 1:
		val, pct, ok := parseTransformOriginToken(parts[0], fontSize)
		if !ok {
			return transformOriginSpec{}, false //nolint:exhaustruct // intentional zero fields
		}

		tok := strings.ToLower(parts[0])
		if tok == cssVerticalAlignTop || tok == cssVerticalAlignBottom {
			spec.Y, spec.YPercent = val, pct
			spec.X, spec.XPercent = transformOriginMiddle, true
		} else {
			spec.X, spec.XPercent = val, pct
			spec.Y, spec.YPercent = transformOriginMiddle, true
		}
	case maxTwoValueArgs, maxThreeValueArgs:
		// Optional third value (z) ignored for 2D print.
		if !applyTransformOriginPair(&spec, parts[0], parts[1], fontSize) {
			return transformOriginSpec{}, false //nolint:exhaustruct // intentional zero fields
		}
	}

	return spec, true
}

// parseTransformOriginToken parses one transform-origin value into its value
// and whether it is a percentage.
func parseTransformOriginToken(tok string, fontSize float64) (float64, bool, bool) {
	tok = strings.ToLower(strings.TrimSpace(tok))
	switch tok {
	case floatLeft, cssVerticalAlignTop:
		return 0, true, true
	case fxCenter:
		return transformOriginMiddle, true, true
	case floatRight, cssVerticalAlignBottom:
		return oneHundred, true, true
	}

	if lv, unit, lok := css.ParseLength(tok); lok && unit == "%" {
		return lv, true, true
	}

	if pt, _, isPct, lok := parseTransformLength(tok, fontSize); lok && !isPct {
		return pt, false, true
	}

	return 0, false, false
}

// applyTransformOriginPair resolves a two-value horizontal/vertical origin
// pair, swapping axes when the values come in vertical-first order.
func applyTransformOriginPair(spec *transformOriginSpec, first, second string, fontSize float64) bool {
	firstTok, secondTok := strings.ToLower(first), strings.ToLower(second)
	firstIsY := firstTok == cssVerticalAlignTop || firstTok == cssVerticalAlignBottom
	secondIsY := secondTok == cssVerticalAlignTop || secondTok == cssVerticalAlignBottom
	secondIsX := secondTok == floatLeft || secondTok == floatRight

	valA, pageA, oka := parseTransformOriginToken(first, fontSize)
	valB, pbox, okb := parseTransformOriginToken(second, fontSize)

	if !oka || !okb {
		return false
	}

	if firstIsY && (secondIsX || !secondIsY) {
		// vertical horizontal → swap
		spec.Y, spec.YPercent = valA, pageA
		spec.X, spec.XPercent = valB, pbox
	} else {
		spec.X, spec.XPercent = valA, pageA
		spec.Y, spec.YPercent = valB, pbox
	}

	return true
}

// parseOpacityValue parses CSS opacity (number 0..1 or percentage).
func parseOpacityValue(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1, false
	}

	if strings.HasSuffix(value, "%") {
		v, err := strconv.ParseFloat(strings.TrimSpace(value[:len(value)-1]), 64)
		if err != nil {
			return 1, false
		}

		return clamp01(v / oneHundred), true
	}

	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 1, false
	}

	return clamp01(v), true
}

// parseFilterOpacity extracts opacity() from a filter list; other functions
// are ignored (blur/drop-shadow are permanent print-engine non-goals).
func parseFilterOpacity(value string) (float64, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == cssDisplayNone {
		return 1, false
	}

	found := false
	acc := 1.0
	rest := value

	for {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			break
		}

		name, args, next, ok := splitTransformFunc(rest)
		if !ok {
			break
		}

		if name == "opacity" {
			if v, pok := parseOpacityValue(args); pok {
				acc *= v
				found = true
			}
		}
		// blur, brightness, etc. ignored
		rest = next
	}

	return acc, found
}

func clamp01(val float64) float64 {
	if val < 0 {
		return 0
	}

	if val > 1 {
		return 1
	}

	return val
}

// isRotateAxisToken reports whether tok is a bare rotate axis name.
func isRotateAxisToken(tok string) bool {
	axis := strings.ToLower(tok)

	return axis == "x" || axis == "y" || axis == "z"
}

// rotateAngleToken extracts the 2D angle token from a CSS rotate value.
// CSS rotate may be "<angle>" or "z <angle>" or "<axis> <angle>" (3d ignored);
// for 2D print the last angle token wins unless it is a bare axis name.
func rotateAngleToken(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)

	if trimmed == "" || strings.EqualFold(trimmed, cssDisplayNone) || strings.EqualFold(trimmed, "initial") {
		return "", false
	}

	fields := strings.Fields(trimmed)

	if len(fields) == 0 {
		return "", false
	}

	angleTok := fields[len(fields)-1]

	if len(fields) > 1 && isRotateAxisToken(angleTok) {
		// e.g. "rotate: z 12deg" weird order – fall back to first angle
		angleTok = fields[0]
	}

	return angleTok, true
}

// parseRotateAngle parses one CSS rotate angle into degrees.
func parseRotateAngle(value string) (float64, bool) {
	angleTok, ok := rotateAngleToken(value)
	if !ok {
		return 0, false
	}

	if deg, degOK := parseAngleDeg(angleTok); degOK {
		return deg, true
	}

	// Try legacy bare numbers that parseAngleDeg already handles; second
	// fallback via ParseFloat (treat as deg) for author convenience.
	if deg, err := strconv.ParseFloat(strings.TrimSpace(angleTok), 64); err == nil {
		return deg, true
	}

	return 0, false
}

// ApplyRotate implements the CSS `rotate` longhand (CSS Transforms Level 2).
// A bare angle stays the 2D path. An axis keyword or a three-number axis
// builds the same side matrix as rotateX, rotateY, rotateZ, or rotate3d.
// On invalid input the style is unchanged and false is returned.
func ApplyRotate(style *ResolvedStyle, value string) bool {
	if style == nil {
		return false
	}

	fields := strings.Fields(strings.ReplaceAll(strings.TrimSpace(value), ",", " "))
	if len(fields) == 1 {
		deg, angleOK := parseRotateAngle(fields[0])
		if !angleOK {
			return false
		}

		style.Transform = style.Transform.Mul(RotateDeg(deg))
		style.HasTransform = true

		return true
	}

	side, ok := parseRotateAxisProperty(fields)
	if !ok {
		return false
	}

	appendTransform3D(style, side)
	style.HasTransform = true

	return true
}

// parseRotateAxisProperty reads "y 60deg", "60deg y", or "0 1 0 60deg".
// A zero three-number axis is rejected.
//
//nolint:cyclop // axis keyword and three-number forms stay in one parser
func parseRotateAxisProperty(fields []string) (matrix3D, bool) {
	angleAt := -1

	for idx := len(fields) - 1; idx >= 0; idx-- {
		field := fields[idx]
		if _, ok := parseAngleDeg(field); ok {
			angleAt = idx

			break
		}
	}

	if angleAt < 0 {
		return matrix3D{}, false
	}

	deg, _ := parseAngleDeg(fields[angleAt])
	axis := make([]string, 0, len(fields)-1)

	for idx, field := range fields {
		if idx == angleAt {
			continue
		}

		axis = append(axis, field)
	}

	switch len(axis) {
	case 1:
		switch strings.ToLower(axis[0]) {
		case "x":
			return rotateX3D(deg), true
		case "y":
			return rotateY3D(deg), true
		case "z":
			return rotateZ3D(deg), true
		default:
			return matrix3D{}, false
		}
	case maxThreeValueArgs:
		return rotatePropertyAxis(axis, deg)
	default:
		return matrix3D{}, false
	}
}

func rotatePropertyAxis(axis []string, deg float64) (matrix3D, bool) {
	nums := make([]float64, len(axis))

	for idx, field := range axis {
		num, ok := parseUnitless(field)
		if !ok {
			return matrix3D{}, false
		}

		nums[idx] = num
	}

	length := math.Sqrt(nums[0]*nums[0] + nums[1]*nums[1] + nums[2]*nums[2])
	if length < axisEpsilon {
		return matrix3D{}, false
	}

	return rotate3DAxis(nums[0]/length, nums[1]/length, nums[2]/length, deg), true
}

// ApplyScale implements the CSS `scale` longhand. It parses one or two
// unitless numbers (sx [sy]); a single number yields uniform scale.
// Invalid input leaves style unchanged. Composition is post-multiply like
// parseTransformList.
//
//nolint:cyclop // the third value is the only addition to the existing 2D parser
func ApplyScale(style *ResolvedStyle, value string) bool {
	if style == nil {
		return false
	}

	trimmed := strings.TrimSpace(value)

	if trimmed == "" || strings.EqualFold(trimmed, cssDisplayNone) || strings.EqualFold(trimmed, "initial") {
		return false
	}

	// scale may be comma-separated in some authoring; normalize to spaces.
	normalized := strings.ReplaceAll(trimmed, ",", " ")
	parts := strings.Fields(normalized)

	if len(parts) == 0 || len(parts) > maxThreeValueArgs {
		return false
	}

	scaleX, scaleOK := parseUnitless(parts[0])
	if !scaleOK {
		return false
	}

	scaleY := scaleX

	if len(parts) >= maxTwoValueArgs {
		second, secondOK := parseUnitless(parts[1])

		if !secondOK {
			return false
		}

		scaleY = second
	}

	if len(parts) == maxThreeValueArgs {
		scaleZ, scaleZOK := parseUnitless(parts[2])
		if !scaleZOK {
			return false
		}

		appendTransform3D(style, scaleZ3D(scaleZ))
	}

	scaled := Scale(scaleX, scaleY)
	style.Transform = style.Transform.Mul(scaled)
	style.HasTransform = true

	return true
}

// parseTranslateValues parses one or two translate lengths from already-split
// fields. The third (z) value is ignored by the caller for 2D print.
func parseTranslateValues(parts []string, fsize float64) (float64, float64, float64, float64, bool, bool, bool) {
	xLen, xPctVal, xPctSet, xOK := parseTransformLength(parts[0], fsize)
	if !xOK {
		return 0, 0, 0, 0, false, false, false
	}

	if len(parts) < maxTwoValueArgs {
		return xLen, 0, xPctVal, math.NaN(), xPctSet, false, true
	}

	yLen, yPctVal, yPctSet, yOK := parseTransformLength(parts[1], fsize)
	if !yOK {
		return 0, 0, 0, 0, false, false, false
	}

	return xLen, yLen, xPctVal, yPctVal, xPctSet, yPctSet, true
}

// ApplyTranslate implements the CSS `translate` longhand (CSS Transforms Level 2).
// It parses one or two lengths (tx [ty]); percentages are deferred and resolved
// against the border box at layout time, bare numbers are treated as px. The
// absolute part is post-multiplied into style.Transform and the percent part
// is stored in TranslateX/YPercent for resolution in stampBoxTransforms.
//
//nolint:cyclop // the third value is the only addition to the existing 2D parser
func ApplyTranslate(style *ResolvedStyle, value string, fsize float64) bool {
	if style == nil {
		return false
	}

	trimmed := strings.TrimSpace(value)

	if trimmed == "" || strings.EqualFold(trimmed, cssDisplayNone) || strings.EqualFold(trimmed, "initial") {
		return false
	}

	normalized := strings.ReplaceAll(trimmed, ",", " ")
	parts := strings.Fields(normalized)

	if len(parts) == 0 || len(parts) > maxThreeValueArgs {
		return false
	}

	transX, transY, xPct, yPct, xIsPct, yIsPct, parseOK := parseTranslateValues(parts, fsize)
	if !parseOK {
		return false
	}

	if len(parts) == maxThreeValueArgs {
		depth, _, depthPercent, depthOK := parseTransformLength(parts[2], fsize)
		if !depthOK || depthPercent {
			return false
		}

		appendTransform3D(style, translateZ3D(depth))
	}

	formed := Translate(transX, transY)
	style.Transform = style.Transform.Mul(formed)

	if xIsPct {
		style.TranslateXPercent = xPct
		style.TranslateXPercentSet = true
	}

	if yIsPct {
		style.TranslateYPercent = yPct
		style.TranslateYPercentSet = true
	}

	style.HasTransform = true

	return true
}

// stampState is the transform state a stamp walk carries into one box. It is
// the part of a stamp run that a subtree root needs from the boxes above it,
// so a restamp can start in the middle of the tree with the same state the
// full walk would have had.
type stampState struct {
	// parentAccum is the matrix stamped on the parent's exclusive operations,
	// the base a flat box composes onto.
	parentAccum Matrix2D
	// viewport is the nearest SVG viewport for transform-box:view-box.
	viewport svgViewport
	// chain, chainActive, chainBase, and worldChain carry the preserve-3d
	// state: the composed 3D chain, whether it is active, the 2D base the
	// chain flattens onto, and the chain in page coordinates.
	chain       matrix3D
	chainActive bool
	chainBase   Matrix2D
	worldChain  matrix3D
}

// initialStampState returns the state a stamp run starts in. The root viewport
// is unset: the root has no SVG ancestor, so a view-box origin on a top-level
// box falls back to the border box.
func initialStampState(parentAccum Matrix2D) stampState {
	return stampState{
		parentAccum: parentAccum,
		viewport:    svgViewport{}, //nolint:exhaustruct // unset viewport
		chain:       identity3D(),
		chainActive: false,
		chainBase:   IdentityMatrix(),
		worldChain:  identity3D(),
	}
}

// stampBoxStep is what one box contributes to a stamp run: the matrix its own
// operations get and the state its children inherit.
type stampBoxStep struct {
	accum          Matrix2D
	parentAccum    Matrix2D
	viewport       svgViewport
	worldChain     matrix3D
	chain          matrix3D
	chainFacing    float64
	useChainFacing bool
	preservePath   bool
	chainActive    bool
	isPreserve     bool
	child          stampState
}

// advanceStamp computes one box's stamp step and the state its children
// inherit. The full walk and the subtree seed both go through it, so a restamp
// cannot drift from the static path.
func advanceStamp(boxNode *box, state stampState) stampBoxStep {
	sty := boxNode.style
	has3D := sty != nil && sty.HasTransform3D
	isPreserve := isPreserveBox(boxNode)

	markPreserveComposed(boxNode, has3D, state.chainActive, isPreserve)

	preservePath := state.chainActive || isPreserve

	boxWorldChain := state.worldChain
	if preservePath {
		boxWorldChain = boxWorldChain.mul(worldTransform3DForBox(boxNode, state.viewport))
	}

	accum2D := boxTransformAccum(boxNode, state.parentAccum, state.viewport)
	accum, childChain, childBase, childActive := preserveStep(
		boxNode, accum2D, state.chain, state.chainActive, isPreserve, state.viewport, state.chainBase)

	childViewport := childViewportForBox(boxNode, state.viewport)

	childParent, childWorldChain := stampChildState(
		preservePath, state.chainActive, childActive, accum, state.parentAccum, boxWorldChain)

	chainFacing, useChainFacing := composedChainFacing(boxNode, state.chain, state.chainActive, has3D, state.viewport)

	return stampBoxStep{
		accum:          accum,
		parentAccum:    state.parentAccum,
		viewport:       state.viewport,
		worldChain:     boxWorldChain,
		chain:          state.chain,
		chainFacing:    chainFacing,
		useChainFacing: useChainFacing,
		preservePath:   preservePath,
		chainActive:    state.chainActive,
		isPreserve:     isPreserve,
		child: stampState{
			parentAccum: childParent,
			viewport:    childViewport,
			chain:       childChain,
			chainActive: childActive,
			chainBase:   childBase,
			worldChain:  childWorldChain,
		},
	}
}

// stampBoxTransforms walks the laid-out tree and stamps composed 2D
// transform matrices (origin baked) onto display-list ops. Parent transforms
// compose around children; sibling flow geometry is unchanged. Preserve-3d
// chains thread unflattened 3D state through the recursion so nested 3D nets
// once instead of per level. Reference: Chrome 143.0.7499.40.
//
// A single []bool covered bitmap replaces per-node map[int]struct{} sets —
// multi-page tables allocate tens of thousands of boxes and the map path was
// a top alloc_space hotspot.
func stampBoxTransforms(boxNode *box, parentAccum Matrix2D, ops []Op, media string) {
	stampTree(boxNode, initialStampState(parentAccum), ops, media, false)
}

// restampBoxTransforms runs the stamp walk on one subtree with a state the
// full walk produced. restamp keeps opacity out of the pass and leaves a
// back-face visible: the operations already carry the opacity their first
// stamp applied, and a culled face cannot be brought back from a rewritten op.
func restampBoxTransforms(boxNode *box, state stampState, ops []Op, media string) {
	stampTree(boxNode, state, ops, media, true)
}

// stampTree allocates the coverage mask and starts one stamp run.
func stampTree(boxNode *box, state stampState, ops []Op, media string, restamp bool) {
	if boxNode == nil || len(ops) == 0 {
		return
	}

	covered := make([]bool, len(ops))

	stampBoxTransformsRec(boxNode, state, ops, covered, media, restamp)
}

// translate3D returns a 3D translation by tx, ty, tz points.
func translate3D(tx, ty, tz float64) matrix3D {
	return matrix3D{
		1, 0, 0, tx,
		0, 1, 0, ty,
		0, 0, 1, tz,
		0, 0, 0, 1,
	}
}

// isPreserveBox reports whether boxNode opts its subtree into the shared 3D
// rendering context. Only preserve-3d continues the chain; flat, missing, or
// unknown styles flatten at the boundary. Reference: Chrome 143.0.7499.40.
func isPreserveBox(boxNode *box) bool {
	return boxNode != nil && boxNode.style != nil && boxNode.style.TransformStyle == "preserve-3d"
}

// baked3DForBox returns the 3D matrix for one box in a form that flatten3D
// can project without leaking the element's page position into the perspective
// scale. The origin-bake is done at the call site (BakeOrigin on the projected
// 2D result), so the matrix passed here is the raw stored 3D transform.
func baked3DForBox(boxNode *box) matrix3D {
	sty := boxNode.style
	if sty == nil || !sty.HasTransform3D {
		return identity3D()
	}

	var raw matrix3D

	copy(raw[:], sty.Transform3D[:])

	return raw
}

// flattenChainForBox projects one composed 3D chain onto the page plane using
// the box perspective camera (inherited ancestor perspective, else the list
// perspective function, else orthographic). Reference: Chrome 143.0.7499.40.
func flattenChainForBox(chain matrix3D, boxNode *box, originX, originY float64) Matrix2D {
	dist, vx, vy := perspectiveFlattenArgs(boxNode, chain, originX, originY)
	proj, _ := chain.flatten3D(vx-originX, vy-originY, dist)

	return proj
}

// preserveStarter starts a preserve-3d chain at a box with no incoming chain.
// With 3D it composes the box alone and bases children on its 2D accum;
// without 3D it carries an empty chain so later 3D children still share one
// flattening context.
func preserveStarter(boxNode *box, accum2D Matrix2D, viewport svgViewport) (Matrix2D, matrix3D, Matrix2D) {
	if boxNode.style == nil || !boxNode.style.HasTransform3D {
		return accum2D, identity3D(), accum2D
	}

	own := baked3DForBox(boxNode)
	sty := boxNode.style
	originX, originY := resolveTransformOriginWithBox(sty.TransformOrigin, boxNode, sty.TransformBox, viewport)
	flat := flattenChainForBox(own, boxNode, originX, originY)
	flat = BakeOrigin(flat, originX, originY)

	return accum2D.Mul(flat), own, accum2D
}

// preserveContinuer extends an active preserve-3d chain with one box. With 3D
// the chain grows and flattens once from the original base; without 3D the
// chain passes through and the box 2D bake folds after the flattened base.
func preserveContinuer(
	boxNode *box, accum2D Matrix2D, chain matrix3D, chainBase Matrix2D, viewport svgViewport,
) (Matrix2D, matrix3D) {
	_ = accum2D

	sty := boxNode.style
	if sty == nil || !sty.HasTransform3D {
		var originX, originY float64
		if sty != nil {
			originX, originY = resolveTransformOriginWithBox(
				sty.TransformOrigin, boxNode, sty.TransformBox, viewport)
		}

		flat := flattenChainForBox(chain, boxNode, originX, originY)
		baked2D := boxTransformAccum(boxNode, IdentityMatrix(), viewport)

		return chainBase.Mul(flat).Mul(baked2D), chain
	}

	own := baked3DForBox(boxNode)
	grown := chain.mul(own)
	originX, originY := resolveTransformOriginWithBox(sty.TransformOrigin, boxNode, sty.TransformBox, viewport)
	flat := flattenChainForBox(grown, boxNode, originX, originY)

	return chainBase.Mul(BakeOrigin(flat, originX, originY)), grown
}

// flatBoundaryAccum flattens an active preserve-3d chain at a flat boundary.
// With 3D the boundary box joins the chain for one final projection; without
// 3D the ancestors flatten and the box 2D bake folds after. Children of both
// start fresh from the returned matrix.
func flatBoundaryAccum(
	boxNode *box, accum2D Matrix2D, chain matrix3D, chainBase Matrix2D, viewport svgViewport,
) Matrix2D {
	_ = accum2D

	sty := boxNode.style
	if sty == nil || !sty.HasTransform3D {
		var originX, originY float64
		if sty != nil {
			originX, originY = resolveTransformOriginWithBox(
				sty.TransformOrigin, boxNode, sty.TransformBox, viewport)
		}

		flat := flattenChainForBox(chain, boxNode, originX, originY)
		baked2D := boxTransformAccum(boxNode, IdentityMatrix(), viewport)

		return chainBase.Mul(flat).Mul(baked2D)
	}

	own := baked3DForBox(boxNode)
	grown := chain.mul(own)
	originX, originY := resolveTransformOriginWithBox(sty.TransformOrigin, boxNode, sty.TransformBox, viewport)
	flat := flattenChainForBox(grown, boxNode, originX, originY)

	return chainBase.Mul(BakeOrigin(flat, originX, originY))
}

// withPercentTranslate folds deferred translate percents into the box transform.
func withPercentTranslate(sty *ResolvedStyle, boxNode *box) Matrix2D {
	tform := sty.Transform

	if !sty.TranslateXPercentSet && !sty.TranslateYPercentSet {
		return tform
	}

	var transX, transY float64

	if sty.TranslateXPercentSet {
		transX = sty.TranslateXPercent / oneHundred * boxNode.w
	}

	if sty.TranslateYPercentSet {
		transY = sty.TranslateYPercent / oneHundred * boxNode.height
	}

	return tform.Mul(Translate(transX, transY))
}

// boxTransformAccum composes one box's baked transform onto its parent.
// viewport is the nearest ancestor SVG viewport: a view-box origin resolves
// against it while every other reference box resolves against the box
// itself (fill-box uses the object bounding box, the border box for CSS
// boxes).
func boxTransformAccum(boxNode *box, parentAccum Matrix2D, viewport svgViewport) Matrix2D {
	sty := boxNode.style

	if sty == nil || !sty.HasTransform {
		return parentAccum
	}

	tform := withPercentTranslate(sty, boxNode)
	originX, originY := resolveTransformOriginWithBox(sty.TransformOrigin, boxNode, sty.TransformBox, viewport)
	baked := BakeOrigin(tform, originX, originY)

	if sty.HasTransform3D && !boxNode.preserveComposed {
		m3 := matrix3D(sty.Transform3D)
		dist, vx, vy := perspectiveFlattenArgs(boxNode, m3, originX, originY)
		proj, _ := m3.flatten3D(vx-originX, vy-originY, dist)
		baked = baked.Mul(BakeOrigin(proj, originX, originY))
	}

	return parentAccum.Mul(baked)
}

// preserveStep selects the stamp matrix and child chain state for one box.
// Flat boxes without a chain use the per-level accum; preserve starters,
// continuers, and flat boundaries delegate to their helpers so the recursion
// stays a thin dispatcher.
func preserveStep(
	boxNode *box, accum2D Matrix2D, chain matrix3D, chainActive, isPreserve bool,
	viewport svgViewport, chainBase Matrix2D,
) (Matrix2D, matrix3D, Matrix2D, bool) {
	switch {
	case !chainActive && !isPreserve:
		return accum2D, identity3D(), IdentityMatrix(), false
	case !chainActive && isPreserve:
		accum, grown, base := preserveStarter(boxNode, accum2D, viewport)

		return accum, grown, base, true
	case chainActive && isPreserve:
		accum, grown := preserveContinuer(boxNode, accum2D, chain, chainBase, viewport)

		return accum, grown, chainBase, true
	default:
		accum := flatBoundaryAccum(boxNode, accum2D, chain, chainBase, viewport)

		return accum, identity3D(), IdentityMatrix(), false
	}
}

// markPreserveComposed flags 3D boxes composed through a preserve chain before
// the per-level branch runs so it keeps the 2D path and the chain owns the
// projection.
func markPreserveComposed(boxNode *box, has3D, chainActive, isPreserve bool) {
	if has3D && (chainActive || isPreserve) {
		boxNode.preserveComposed = true
	}
}

func stampBoxTransformsRec(
	boxNode *box, state stampState, ops []Op, covered []bool, media string, restamp bool,
) {
	if boxNode == nil {
		return
	}

	step := advanceStamp(boxNode, state)

	for _, c := range boxNode.children {
		stampBoxTransformsRec(c, step.child, ops, covered, media, restamp)
	}

	// Mark child-owned ranges, stamp exclusive ops, then clear for siblings.
	markChildrenOpsCovered(boxNode, ops, covered, true)

	stampBoxTransformOps(
		boxNode, step, ops, covered, media, restamp,
	)

	// The first stamp already folded each element's opacity into its
	// operations. A restamp changes transforms only, so folding it again
	// would multiply the same opacity into the same op on every frame.
	if !restamp {
		stampExclusiveOpacityOps(boxNode, ops, covered)
		stampCoveredOpacityOps(boxNode, ops, covered)
	}

	markChildrenOpsCovered(boxNode, ops, covered, false)
}

// childViewportForBox returns the SVG viewport boxNode establishes for its
// subtree, or viewport when the box establishes none.
func childViewportForBox(boxNode *box, viewport svgViewport) svgViewport {
	if established, ok := viewportOfBox(boxNode); ok {
		return established
	}

	return viewport
}

// stampChildState returns the transform state handed to one box's children.
// Preserve children keep the 2D-only parent: the chain carries deferred 3D.
func stampChildState(
	preservePath, chainActive, childActive bool, accum, parentAccum Matrix2D, boxWorldChain matrix3D,
) (Matrix2D, matrix3D) {
	childParent := accum
	if childActive {
		childParent = parentAccum
	} else if preservePath && chainActive {
		childParent = IdentityMatrix()
	}

	childWorldChain := identity3D()
	if childActive {
		childWorldChain = boxWorldChain
	}

	return childParent, childWorldChain
}

// markChildrenOpsCovered marks or clears the display-list ops owned by every
// child of boxNode.
func markChildrenOpsCovered(boxNode *box, ops []Op, covered []bool, record bool) {
	for _, c := range boxNode.children {
		markBoxOpsCovered(c, ops, covered, record)
	}
}

// stampBoxTransformOps applies the box's exclusive ops transform: the shared
// preserve chain when preservePath is set, the per-level 2D accum otherwise.
func stampBoxTransformOps(
	boxNode *box, step stampBoxStep, ops []Op, covered []bool, media string, restamp bool,
) {
	if !step.preservePath {
		stampExclusiveTransformOps(
			boxNode, step.accum, ops, covered, step.chainFacing, step.useChainFacing, media, !restamp)
		stampProjectiveTransformOps(boxNode, step.parentAccum, ops, step.viewport)

		return
	}

	stampExclusiveTransformOps(
		boxNode, IdentityMatrix(), ops, covered, step.chainFacing, step.useChainFacing, media, !restamp)

	exclusive := true
	if step.chainActive && !step.isPreserve {
		exclusive = false
	}

	stampPreserveTransformRange(
		boxNode, step.worldChain, step.parentAccum, ops, covered, exclusive, step.viewport)
}

// projectiveStampForBox returns the projective matrix a box stamps over its
// whole operation range: its own 3D transform flattened through the inherited
// camera, its 2D bake, and the parent accumulation. The boolean is false when
// the box has no 3D transform or no perspective camera to project it with.
func projectiveStampForBox(
	boxNode *box, parentAccum Matrix2D, viewport svgViewport,
) (ProjectiveTransform, bool) {
	if boxNode == nil || boxNode.style == nil || !boxNode.style.HasTransform3D || boxNode.preserveComposed {
		return ProjectiveTransform{}, false //nolint:exhaustruct // no projection
	}

	style := boxNode.style
	originX, originY := resolveTransformOriginWithBox(style.TransformOrigin, boxNode, style.TransformBox, viewport)
	transform := matrix3D(style.Transform3D)
	dist, vanishX, vanishY := perspectiveFlattenArgs(boxNode, transform, originX, originY)

	if dist <= 0 {
		return ProjectiveTransform{}, false //nolint:exhaustruct // orthographic: no projective matrix
	}

	projective := projectiveTransform3D(transform, originX, originY, vanishX, vanishY, dist)
	local := BakeOrigin(withPercentTranslate(style, boxNode), originX, originY)
	projective = composeProjectiveAffine(projective, local)

	return composeProjectiveAffine(projective, parentAccum), true
}

func stampProjectiveTransformOps(
	boxNode *box, parentAccum Matrix2D, ops []Op, viewport svgViewport,
) {
	projective, ok := projectiveStampForBox(boxNode, parentAccum, viewport)
	if !ok {
		return
	}

	end := min(boxNode.opEnd, len(ops)-1)
	for idx := boxNode.opStart; idx <= end; idx++ {
		if idx < 0 || ops[idx].Kind == OpKindNoop {
			continue
		}

		ops[idx].setProjectiveTransform(projective)
	}
}

func composeProjectiveAffine(projective ProjectiveTransform, affine Matrix2D) ProjectiveTransform {
	return ProjectiveTransform{
		A: affine.A*projective.A + affine.C*projective.B + affine.E*projective.G,
		B: affine.B*projective.A + affine.D*projective.B + affine.F*projective.G,
		C: affine.A*projective.C + affine.C*projective.D + affine.E*projective.H,
		D: affine.B*projective.C + affine.D*projective.D + affine.F*projective.H,
		E: affine.A*projective.E + affine.C*projective.F + affine.E*projective.I,
		F: affine.B*projective.E + affine.D*projective.F + affine.F*projective.I,
		G: projective.G, H: projective.H, I: projective.I,
	}
}

// markBoxOpsCovered records or clears the display-list ops owned by child boxNode.
func markBoxOpsCovered(boxNode *box, ops []Op, covered []bool, record bool) {
	if !boxOwnsOps(boxNode) {
		return
	}

	end := boxNode.opEnd
	if end >= len(ops) {
		end = len(ops) - 1
	}

	for idx := boxNode.opStart; idx <= end; idx++ {
		covered[idx] = record
	}
}

// boxOwnsOps reports whether boxNode has a non-empty range of exclusive ops.
func boxOwnsOps(boxNode *box) bool {
	return boxNode.opEnd >= boxNode.opStart && boxNode.opStart >= 0
}

// stampExclusiveTransformOps applies the accumulated transform to ops owned
// exclusively by boxNode; child-owned ops keep the child's own transform.
// A restamp passes cull=false: it must not destroy an operation's geometry,
// because a later frame cannot bring a zeroed op back.
func stampExclusiveTransformOps(
	boxNode *box, accum Matrix2D, ops []Op, covered []bool,
	chainFacing float64, useChainFacing bool, media string, cull bool,
) {
	// Chrome 143.0.7499.40 culls hidden back faces on screen but paints them
	// in print-to-PDF. Keep screen culling here and preserve those ops for print.
	isPrint := strings.EqualFold(strings.TrimSpace(media), "print")
	if cull && !isPrint && backfaceCulled(boxNode, chainFacing, useChainFacing) {
		zeroExclusiveOps(boxNode, ops, covered)

		return
	}

	if accum.IsIdentity() || !boxOwnsOps(boxNode) {
		return
	}

	end := boxNode.opEnd
	if end >= len(ops) {
		end = len(ops) - 1
	}

	for idx := boxNode.opStart; idx <= end; idx++ {
		if covered[idx] {
			continue
		}

		ops[idx].setXform(accum)
		ops[idx].XformSet = true
	}
}

// backfaceCulled reports whether boxNode's exclusive ops must be hidden:
// a 3D-rotated face turned away from the viewer under
// backface-visibility:hidden. Facing comes from the flattened 3D state;
// 2D content never qualifies.
// composedChainFacing reports the facing of the preserve-3d chain this box
// belongs to. use is false when the box is not inside that chain, and the
// caller then reads the local side matrix instead.
func composedChainFacing(
	boxNode *box, chain matrix3D, chainActive, has3D bool, viewport svgViewport,
) (float64, bool) {
	if !chainActive || boxNode == nil {
		return 0, false
	}

	facingChain := chain
	if has3D {
		facingChain = chain.mul(baked3DForBox(boxNode))
	}

	var originX, originY float64
	if boxNode.style != nil {
		originX, originY = resolveTransformOriginWithBox(
			boxNode.style.TransformOrigin, boxNode, boxNode.style.TransformBox, viewport,
		)
	}

	dist, vanishX, vanishY := perspectiveFlattenArgs(boxNode, facingChain, originX, originY)
	_, facing := facingChain.flatten3D(vanishX-originX, vanishY-originY, dist)

	return facing, true
}

func backfaceCulled(boxNode *box, chainFacing float64, useChainFacing bool) bool {
	if boxNode == nil || boxNode.style == nil {
		return false
	}

	if boxNode.style.BackfaceVisibility != "hidden" {
		return false
	}

	if !boxOwnsOps(boxNode) {
		return false
	}

	if useChainFacing {
		return chainFacing < 0
	}

	if !boxNode.style.HasTransform3D {
		return false
	}

	_, facing := matrix3D(boxNode.style.Transform3D).flatten3D(0, 0, 0)

	return facing < 0
}

// zeroExclusiveOps hides one box's exclusive ops by collapsing their
// geometry and deactivating them (Kind = OpKindNoop) so painters skip them,
// skipping child-owned ranges via the covered mask. W and H stay zeroed to
// keep the deactivated contract consistent.
func zeroExclusiveOps(boxNode *box, ops []Op, covered []bool) {
	end := boxNode.opEnd
	if end >= len(ops) {
		end = len(ops) - 1
	}

	for idx := boxNode.opStart; idx <= end; idx++ {
		if covered[idx] {
			continue
		}

		ops[idx].W = 0
		ops[idx].H = 0
		ops[idx].Kind = OpKindNoop
	}
}

// stampExclusiveOpacityOps multiplies boxNode's opacity onto its exclusive ops.
func stampExclusiveOpacityOps(boxNode *box, ops []Op, covered []bool) {
	if boxNode.style == nil || boxNode.style.Opacity >= 1 || !boxOwnsOps(boxNode) {
		return
	}

	end := boxNode.opEnd
	if end >= len(ops) {
		end = len(ops) - 1
	}

	opacityBase := boxNode.style.Opacity

	for idx := boxNode.opStart; idx <= end; idx++ {
		if covered[idx] {
			continue
		}

		opacity := opacityBase
		if ops[idx].PaintOpacity > 0 && ops[idx].PaintOpacity < 1 {
			opacity *= ops[idx].PaintOpacity
		}

		ops[idx].setPaintOpacity(opacity)
	}
}

// stampCoveredOpacityOps composes boxNode's ancestor opacity through
// child-owned ops (opacity composites through descendants).
func stampCoveredOpacityOps(boxNode *box, ops []Op, covered []bool) {
	if boxNode.style == nil || boxNode.style.Opacity >= 1 || !boxOwnsOps(boxNode) {
		return
	}

	end := boxNode.opEnd
	if end >= len(ops) {
		end = len(ops) - 1
	}

	opacityBase := boxNode.style.Opacity

	for idx := boxNode.opStart; idx <= end; idx++ {
		if !covered[idx] {
			continue
		}

		if ops[idx].PaintOpacity > 0 && ops[idx].PaintOpacity < 1 {
			ops[idx].setPaintOpacity(opacityBase * ops[idx].PaintOpacity)
		} else {
			ops[idx].setPaintOpacity(opacityBase)
		}
	}
}
