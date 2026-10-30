//nolint:varnamelen // shape-inside / shape-padding lite parsers
package layout

import "github.com/chinmay-sawant/blinkless/internal/css"

// CSS Shapes inside apply group (css-shapes-2 lite, v0.0.3 shapes ledger):
//   - shape-inside: auto | outside-shape | circle() | ellipse() | inset()
//     with an optional shape-box keyword
//   - shape-padding: <length-percentage>, shrinks the inside contour
//
// polygon(), path(), <image> (url()), and display parse as rejected and leave
// the earlier stored value, the same rule shape-outside follows
// (style_shape_props.go). shape-box keywords are stored here and DO change
// the reference rectangle, unlike shape-outside where the keyword is stored
// and ignored at place time (style_shape_props.go:47-48). Chrome has no BCD
// support for either property; this is an engine-lite subset, not a parity
// claim.

const (
	shapeInsideAuto         = "auto"
	shapeInsideOutsideShape = "outside-shape"
)

// applyShapeInsideProps owns shape-inside and shape-padding.
func applyShapeInsideProps(
	style *ResolvedStyle, prop, value string, fsize float64, _ *styleContext, _ *ResolvedStyle, _ bool,
) bool {
	switch prop {
	case "shape-inside":
		if inside, box, outside, ok := parseShapeInside(value); ok {
			style.ShapeInside = inside
			style.ShapeInsideBox = box
			style.ShapeInsideOutside = outside
		}
	case "shape-padding":
		if pt, pct, ok := parseShapePadding(value, fsize); ok {
			style.ShapePadding = pt
			style.ShapePaddingPercent = pct
		}
	default:
		return false
	}

	return true
}

// parseShapeInside validates and canonicalizes the supported shape-inside
// subset. auto clears the inside shape; outside-shape sets the copy flag and
// leaves ShapeInside at auto (the flag is the discriminator). A rejected
// token reports ok = false so the caller keeps the earlier stored value.
func parseShapeInside(raw string) (string, string, bool, bool) {
	value := normalizeCSSValue(raw)
	if value == "" {
		return "", "", false, false
	}

	if value == shapeInsideAuto {
		return shapeInsideAuto, "", false, true
	}

	if value == shapeInsideOutsideShape {
		return shapeInsideAuto, "", true, true
	}

	shape, box, ok := splitShapeOutsideParts(value)
	if !ok {
		return "", "", false, false
	}

	name, args, ok := splitShapeFunction(shape)
	if !ok {
		return "", "", false, false
	}

	canonical, ok := canonicalShapeInsideFunction(name, args)
	if !ok {
		return "", "", false, false
	}

	return canonical, box, false, true
}

// canonicalShapeInsideFunction canonicalizes the supported shape functions.
// polygon(), path(), url() images, and display report ok = false.
func canonicalShapeInsideFunction(name, args string) (string, bool) {
	switch name {
	case "circle":
		return canonicalShapeCircle(args)
	case shapeEllipseKeyword:
		return canonicalShapeEllipse(args)
	case "inset":
		return canonicalShapeInset(args)
	default:
		return "", false
	}
}

// parseShapePadding accepts a non-negative length or percentage. The
// percentage stay stores as-is; the consumer resolves it against the shape
// reference diagonal the way shape-margin does (shape_inside.go). A negative
// or unparseable value reports ok = false so the earlier padding survives.
func parseShapePadding(raw string, fsize float64) (float64, float64, bool) {
	value := normalizeCSSValue(raw)
	if value == "" {
		return 0, -1, false
	}

	val, unit, ok := css.ParseLength(value)
	if !ok || val < 0 {
		return 0, -1, false
	}

	if unit == "%" {
		return 0, val, true
	}

	pt, converted := lengthToPt(val, unit, fsize)
	if !converted {
		return 0, -1, false
	}

	return pt, -1, true
}
