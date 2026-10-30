// Package owncode carries the files embedded into the binary: the HTML and
// CSS template set, the sample diff fixture, and the sample workspace.
// They live at the repository root so templates, fixtures, and test data
// stay visible; internal packages consume them through these variables.
package owncode

import "embed"

// Components is the HTML fragment and stylesheet set the app renders.
// Templates stay at the repository root; application code lives in internal/.

//go:embed components/html/*.html components/css/*.css
var Components embed.FS

// SampleDiff is the checked-in unified diff for the bundled workspace.
// It is the default Source Control view so the app has a rich diff
// without a repository. With -root, live git replaces it.

//go:embed fixtures/sample.diff
var SampleDiff []byte

// SampleFiles is the bundled demo project the viewer opens by default.
// `-root` replaces it with a real directory.

//go:embed testdata/sample
var SampleFiles embed.FS
