package layout

import "errors"

var (
	// ErrNilContext means DisplayList was called without a context.
	ErrNilContext = errors.New("layout: nil context")

	// ErrNilDocument means DisplayList was given no styled document, or
	// SnapDisplayToDevicePixels was given no display.
	ErrNilDocument = errors.New("layout: nil document")

	// ErrBadDeviceScale means SnapDisplayToDevicePixels was given a device
	// scale factor that is not finite or not positive.
	ErrBadDeviceScale = errors.New("layout: bad device scale factor")
)
