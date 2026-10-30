// Package errs provides canonical domain and operational sentinel errors for blinkless.
package errs

import "errors"

// Shared canonical sentinel errors across all packages.
// Deprecated hub: prefer package-local sentinels.
// This hub is retained for compatibility until all consumers migrate.
// See PT-GO-28.
//
// Migrated (primary definitions now in owning packages, errs not used):
//   - ErrNilLoader -> load.ErrNilLoader
//
// The former app and image output packages and their sentinels
// (errNilRequest, errImagesDisabled, ErrMissingOutput) are gone from this tree.
//
// Parked (still in hub, need coordinated migration):
//   - ErrNilContext (10+ consumers across load/convert/prepare/render and
//     other packages; distinct instances would break errors.Is)
//   - ErrNilCommand (parked; no consumer in this tree)
var (
	// ErrNilContext is returned when a cancellation-aware operation receives a nil context.
	ErrNilContext = errors.New("blinkless: nil context")
	// ErrNilCommand is a parked sentinel with no consumer in this tree.
	// Deprecated: do not use in new code.
	ErrNilCommand = errors.New("blinkless: nil command")
)
