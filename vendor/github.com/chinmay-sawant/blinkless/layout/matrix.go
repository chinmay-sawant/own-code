package layout

import ilayout "github.com/chinmay-sawant/blinkless/internal/layout"

// Matrix2D is a CSS/SVG-style 2D affine transform:
//
//	x' = A*x + C*y + E
//	y' = B*x + D*y + F
//
// It is the same type the engine uses for an operation's baked transform, so a
// host can compose an animation matrix without copying fields through a
// DisplayOp.
type Matrix2D = ilayout.Matrix2D

// IdentityMatrix returns the identity 2D transform.
func IdentityMatrix() Matrix2D {
	return ilayout.IdentityMatrix()
}

// BakeOrigin composes m so it applies about (ox, oy):
// T(ox, oy) * m * T(-ox, -oy).
func BakeOrigin(m Matrix2D, ox, oy float64) Matrix2D {
	return ilayout.BakeOrigin(m, ox, oy)
}
