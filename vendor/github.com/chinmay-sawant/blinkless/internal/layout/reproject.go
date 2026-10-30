package layout

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/chinmay-sawant/blinkless/internal/errs"
)

// The restamp pass re-projects one or more element subtrees without running
// the cascade or the placement again. It exists because a 3D transform and a
// perspective property have no 2D affine equivalent: the projection is computed
// across a whole subtree at stamp time, so an animation of either one has to
// re-run that stamp instead of replacing a matrix.
//
// The pass mutates the retained placement in place, exactly as RelayoutSubtree
// does, and returns the operation windows it rewrote. A caller repaints those
// windows and adopts the new display the public wrapper builds.
var (
	// ErrNoElement means a restamp override named an id no laid-out box
	// carries.
	ErrNoElement = errors.New("layout: no element with that id")

	// ErrInvalidTransform means a restamp override carried a transform list
	// the cascade parser rejects. A malformed override is reported once and
	// changes nothing; a page never becomes half restamped.
	ErrInvalidTransform = errors.New("layout: invalid transform override")

	// ErrNoRetainedState means the restamp pass was given a placement that
	// keeps no box tree. A display only keeps one when it was laid out with
	// Options.RetainState.
	ErrNoRetainedState = errors.New("layout: display carries no retained layout state")

	// ErrNotStampable means the element cannot be re-projected on its own:
	// its box is not reachable through the children chain the stamp walk
	// follows, or the static path never stamps a transform declared on it
	// (a table cell).
	ErrNotStampable = errors.New("layout: element subtree cannot be restamped")

	// errInvalidPerspective means an override carried a negative or
	// non-finite perspective distance.
	errInvalidPerspective = errors.New("layout: invalid perspective override")
)

// RestampOverride replaces resolved transform values for one element during
// one restamp pass. It is the engine-side form of the public
// layout.TransformOverride.
//
// OriginX, OriginY, Perspective, and the perspective origin are in layout
// points. The origins are page coordinates: the vanishing point or the
// transform origin the stamp walk resolves to, not an offset inside the box.
type RestampOverride struct {
	ElementID string
	// Transform is a raw CSS transform list. HasTransform=false leaves the
	// element's transform untouched; HasTransform=true with "" means none.
	Transform    string
	HasTransform bool
	// OriginX and OriginY replace transform-origin in layout points.
	OriginX   float64
	OriginY   float64
	HasOrigin bool
	// Perspective replaces the perspective property this element gives its
	// descendants, in layout points. Zero means none.
	Perspective    float64
	HasPerspective bool
	// PerspectiveOriginX and PerspectiveOriginY replace perspective-origin
	// in layout points.
	PerspectiveOriginX   float64
	PerspectiveOriginY   float64
	HasPerspectiveOrigin bool
}

// OpWindow is one inclusive operation range a restamp pass rewrote.
type OpWindow struct {
	Start int
	End   int
}

// RestampPatch is the result of a restamp pass.
type RestampPatch struct {
	// Changed reports whether any override actually replaced a resolved
	// value. When it is false nothing was stamped and Windows is empty.
	Changed bool
	// Windows are the patched operation ranges, in page order.
	Windows []OpWindow
	// OldX, OldY, OldW, and OldH bound the painted area of the patched
	// subtrees before the pass, in layout points.
	OldX, OldY, OldW, OldH float64
	// NewX, NewY, NewW, and NewH bound that area after the pass, in layout
	// points.
	NewX, NewY, NewW, NewH float64
}

// RestampTransforms re-runs the perspective pre-pass and the transform stamp
// walk for the subtrees named by overrides, inside a placement that is already
// laid out. It never re-cascades and never re-places: the boxes keep their
// geometry, and only the transform state stamped on their operations changes.
//
// The overrides are applied for this pass only. The cached ResolvedStyle of
// every element is left untouched, so the next static frame is the one the
// cascade produced.
//
// An override list with no effective change returns Changed=false and leaves
// every operation as it was. Nested overrides are supported: an inner target
// is stamped inside the outer target's subtree walk, so the inner value wins
// for its own subtree and inherits the outer camera.
func RestampTransforms(
	ctx context.Context, prev *Result, overrides []RestampOverride, opts Options,
) (*RestampPatch, error) {
	ready, err := restampReady(ctx, prev, overrides)
	if err != nil {
		return nil, err
	}

	if !ready {
		return &RestampPatch{}, nil //nolint:exhaustruct // nothing to do
	}

	plan, err := planRestamp(prev, overrides)
	if err != nil {
		return nil, err
	}

	if !plan.changed {
		return &RestampPatch{}, nil //nolint:exhaustruct // nothing changed
	}

	roots := plan.outermost(prev.boxTreeRoot())
	if len(roots) == 0 {
		return &RestampPatch{}, nil //nolint:exhaustruct // nothing changed
	}

	// The styles are swapped in before the seed walk: an ancestor of a
	// nested target has to show the effective perspective it overrides.
	plan.install()

	defer plan.restore()

	states, err := restampStates(prev, roots)
	if err != nil {
		return nil, err
	}

	if len(states) == 0 {
		return &RestampPatch{}, nil //nolint:exhaustruct // nothing painted
	}

	return applyRestamp(prev, states, opts.Media)
}

// restampReady rejects the inputs the pass cannot run on. A false answer
// without an error means there is nothing to do.
func restampReady(ctx context.Context, prev *Result, overrides []RestampOverride) (bool, error) {
	if ctx == nil {
		return false, errs.ErrNilContext
	}

	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("layout: context: %w", err)
	}

	if len(overrides) == 0 {
		return false, nil
	}

	if prev == nil || prev.boxTreeRoot() == nil {
		return false, ErrNoRetainedState
	}

	return true, nil
}

// restampStates resolves the seed state and the ancestor stamps of every
// outermost subtree, and the operation window each one owns.
func restampStates(prev *Result, roots []*restampTarget) ([]subtreeStamp, error) {
	states := make([]subtreeStamp, 0, len(roots))

	for _, target := range roots {
		state, actions, hasState := stampStateAt(prev.boxTreeRoot(), target.box)
		if !hasState {
			return nil, fmt.Errorf("%w: %s", ErrNotStampable, target.id)
		}

		window, hasWindow := opWindowFor(target.box, len(prev.Ops))
		if !hasWindow {
			continue
		}

		states = append(states, subtreeStamp{
			box:     target.box,
			window:  window,
			state:   state,
			actions: actions,
		})
	}

	return states, nil
}

// applyRestamp rewrites every subtree window and finalizes the projected
// geometry it produced. A failure rolls every window back, because a display
// list that is half projected paints wrong pixels.
func applyRestamp(prev *Result, states []subtreeStamp, media string) (*RestampPatch, error) {
	windows := make([]OpWindow, 0, len(states))
	for idx := range states {
		windows = append(windows, states[idx].window)
	}

	oldBounds := unionWindowBounds(prev.Ops, windows)

	backups := make([]opBackup, 0, len(windows))
	committed := false

	defer func() {
		if committed {
			return
		}

		for idx := range backups {
			copy(prev.Ops[backups[idx].start:], backups[idx].ops)
		}
	}()

	for idx := range windows {
		backups = append(backups, backupWindow(prev.Ops, windows[idx]))
	}

	for idx := range states {
		if err := restampSubtree(prev, &states[idx], media); err != nil {
			return nil, err
		}
	}

	committed = true

	newBounds := unionWindowBounds(prev.Ops, windows)

	return &RestampPatch{
		Changed: true,
		Windows: windows,
		OldX:    oldBounds.x,
		OldY:    oldBounds.y,
		OldW:    oldBounds.w,
		OldH:    oldBounds.h,
		NewX:    newBounds.x,
		NewY:    newBounds.y,
		NewW:    newBounds.w,
		NewH:    newBounds.h,
	}, nil
}

// restampSubtree resets and re-stamps one subtree window.
func restampSubtree(prev *Result, item *subtreeStamp, media string) error {
	resetStampFields(item.box)
	propagatePerspectiveFrom(item.box, nearestPerspectiveAncestor(prev.boxTreeRoot(), item.box))
	resetWindowTransforms(prev.Ops, item.window)

	restampBoxTransforms(item.box, item.state, prev.Ops, media)

	for actionIdx := range item.actions {
		applyAncestorStamp(item.actions[actionIdx], prev.Ops, item.window)
	}

	if err := FinalizeDisplayOps(prev.Ops[item.window.Start : item.window.End+1]); err != nil {
		return fmt.Errorf("layout: restamp: %w", err)
	}

	return nil
}

// restampTarget is one element an override applies to, with the effective
// style this pass stamps.
type restampTarget struct {
	id       string
	box      *box
	original *ResolvedStyle
	style    *ResolvedStyle
}

// restampPlan is the resolved override list.
type restampPlan struct {
	targets []restampTarget
	changed bool
}

// planRestamp resolves every override against the retained box tree and builds
// one effective style per element. Overrides for the same element apply in
// order: the last one wins for a field it sets.
func planRestamp(prev *Result, overrides []RestampOverride) (*restampPlan, error) {
	plan := &restampPlan{ //nolint:exhaustruct // an empty plan
		targets: make([]restampTarget, 0, len(overrides)),
	}
	byBox := make(map[*box]int, len(overrides))

	for idx := range overrides {
		override := &overrides[idx]

		if override.ElementID == "" {
			return nil, fmt.Errorf("%w: empty element id", ErrNoElement)
		}

		boxNode := prev.boxForID(override.ElementID)
		if boxNode == nil {
			return nil, fmt.Errorf("%w: %s", ErrNoElement, override.ElementID)
		}

		if boxNode.style == nil {
			return nil, fmt.Errorf("%w: %s has no resolved style", ErrNotStampable, override.ElementID)
		}

		// A table cell is placed by the table layout, and the static path
		// does not run the stamp walk for a transform declared on the cell
		// alone. Restamping it would paint a projection the authored page
		// never shows, so the pass refuses it.
		if boxNode.kind == boxKindCell {
			return nil, fmt.Errorf("%w: %s is a table cell", ErrNotStampable, override.ElementID)
		}

		target := plan.targetFor(boxNode, override.ElementID, byBox)
		if err := applyRestampOverride(target.style, target.box, override); err != nil {
			return nil, err
		}
	}

	for idx := range plan.targets {
		if !sameTransformState(plan.targets[idx].style, plan.targets[idx].original) {
			plan.changed = true

			break
		}
	}

	return plan, nil
}

// targetFor returns the plan entry for one box, cloning its cached style on
// first use.
func (p *restampPlan) targetFor(boxNode *box, elementID string, byBox map[*box]int) *restampTarget {
	if index, ok := byBox[boxNode]; ok {
		return &p.targets[index]
	}

	clone := *boxNode.style
	byBox[boxNode] = len(p.targets)

	p.targets = append(p.targets, restampTarget{
		id:       elementID,
		box:      boxNode,
		original: boxNode.style,
		style:    &clone,
	})

	return &p.targets[len(p.targets)-1]
}

// install points every target box at its effective style for the pass.
func (p *restampPlan) install() {
	for idx := range p.targets {
		p.targets[idx].box.style = p.targets[idx].style
	}
}

// restore puts the cached styles back.
func (p *restampPlan) restore() {
	for idx := len(p.targets) - 1; idx >= 0; idx-- {
		p.targets[idx].box.style = p.targets[idx].original
	}
}

// outermost returns the targets that no other target contains, in document
// order. A nested target is stamped inside its outer target's subtree walk.
func (p *restampPlan) outermost(root *box) []*restampTarget {
	targets := make(map[*box]int, len(p.targets))
	for idx := range p.targets {
		targets[p.targets[idx].box] = idx
	}

	seen := make(map[*box]bool, len(p.targets))
	out := make([]*restampTarget, 0, len(p.targets))

	var walk func(boxNode *box, inside bool)
	walk = func(boxNode *box, inside bool) {
		index, isTarget := targets[boxNode]

		if isTarget {
			seen[boxNode] = true

			if !inside {
				out = append(out, &p.targets[index])
			}
		}

		next := inside || isTarget

		for _, child := range boxNode.children {
			walk(child, next)
		}
	}

	walk(root, false)

	// A box that hangs off the children chain (a table cell lives in its row
	// list) is handed back too, so the caller reports it as not stampable
	// instead of silently ignoring the override.
	for idx := range p.targets {
		if !seen[p.targets[idx].box] {
			out = append(out, &p.targets[idx])
		}
	}

	return out
}

// applyRestampOverride writes one override onto an effective style.
func applyRestampOverride(style *ResolvedStyle, boxNode *box, override *RestampOverride) error {
	if override.HasTransform {
		if err := applyTransformOverride(style, override.Transform); err != nil {
			return err
		}
	}

	if override.HasOrigin {
		style.applyOriginOverride(boxNode, override.OriginX, override.OriginY)
	}

	if override.HasPerspective {
		if err := style.applyPerspectiveOverride(override.Perspective); err != nil {
			return err
		}
	}

	if override.HasPerspectiveOrigin {
		style.applyPerspectiveOriginOverride(boxNode, override.PerspectiveOriginX, override.PerspectiveOriginY)
	}

	return nil
}

// applyTransformOverride replaces the element's used transform with one parsed
// CSS list. The cascade parser is reused, so a 2D and a 3D function take the
// same path they take in a stylesheet.
func applyTransformOverride(style *ResolvedStyle, value string) error {
	transform := strings.TrimSpace(value)

	matrix, xPercent, yPercent, has, ok, accum3D, has3D := parseTransformList3D(transform, style.FontSize)
	if !ok && transform != "" {
		return fmt.Errorf("%w: %q", ErrInvalidTransform, value)
	}

	if transform == "" {
		matrix, has = IdentityMatrix(), false
	}

	style.Transform = matrix
	style.HasTransform = has
	style.Transform3D = accum3D
	style.HasTransform3D = has3D

	style.TranslateXPercent, style.TranslateXPercentSet = 0, false
	if !math.IsNaN(xPercent) {
		style.TranslateXPercent, style.TranslateXPercentSet = xPercent, true
	}

	style.TranslateYPercent, style.TranslateYPercentSet = 0, false
	if !math.IsNaN(yPercent) {
		style.TranslateYPercent, style.TranslateYPercentSet = yPercent, true
	}

	return nil
}

// applyOriginOverride replaces transform-origin with the resolved page point
// the caller hands back. The offset from the border box lands on that point, so
// the override bypasses the transform-box reference box.
func (style *ResolvedStyle) applyOriginOverride(boxNode *box, originX, originY float64) {
	style.TransformOrigin = transformOriginSpec{
		X:        originX - boxNode.x,
		Y:        originY - boxNode.y,
		XPercent: false,
		YPercent: false,
	}
	style.TransformBox = "border-box"
}

// applyPerspectiveOverride replaces the perspective distance in layout points.
// Zero means none.
func (style *ResolvedStyle) applyPerspectiveOverride(distance float64) error {
	if math.IsNaN(distance) || math.IsInf(distance, 0) || distance < 0 {
		return fmt.Errorf("%w: %g", errInvalidPerspective, distance)
	}

	style.PerspectiveDist = distance
	style.HasPerspective = distance > 0

	return nil
}

// applyPerspectiveOriginOverride replaces perspective-origin with the resolved
// page point the caller hands back.
func (style *ResolvedStyle) applyPerspectiveOriginOverride(boxNode *box, originX, originY float64) {
	style.PerspectiveOriginX = originX - boxNode.x
	style.PerspectiveOriginXPct = false
	style.PerspectiveOriginY = originY - boxNode.y
	style.PerspectiveOriginYPct = false
	style.PerspectiveOriginSet = true
}

// restampStyleValues is the transform slice of a ResolvedStyle. It is a
// comparable snapshot so a no-op override can be detected with one compare.
type restampStyleValues struct {
	transform             Matrix2D
	origin                transformOriginSpec
	transform3D           [16]float64
	translateXPercent     float64
	translateYPercent     float64
	perspective           float64
	perspectiveOriginX    float64
	perspectiveOriginY    float64
	transformBox          string
	hasTransform          bool
	hasTransform3D        bool
	translateXPercentSet  bool
	translateYPercentSet  bool
	hasPerspective        bool
	perspectiveOriginXPct bool
	perspectiveOriginYPct bool
	perspectiveOriginSet  bool
}

// styleValues snapshots the transform state one style declares.
func styleValues(style *ResolvedStyle) restampStyleValues {
	return restampStyleValues{
		transform:             style.Transform,
		origin:                style.TransformOrigin,
		transform3D:           style.Transform3D,
		translateXPercent:     style.TranslateXPercent,
		translateYPercent:     style.TranslateYPercent,
		perspective:           style.PerspectiveDist,
		perspectiveOriginX:    style.PerspectiveOriginX,
		perspectiveOriginY:    style.PerspectiveOriginY,
		transformBox:          style.TransformBox,
		hasTransform:          style.HasTransform,
		hasTransform3D:        style.HasTransform3D,
		translateXPercentSet:  style.TranslateXPercentSet,
		translateYPercentSet:  style.TranslateYPercentSet,
		hasPerspective:        style.HasPerspective,
		perspectiveOriginXPct: style.PerspectiveOriginXPct,
		perspectiveOriginYPct: style.PerspectiveOriginYPct,
		perspectiveOriginSet:  style.PerspectiveOriginSet,
	}
}

// sameTransformState reports whether the effective style resolves to the same
// transform values as the cached style. A no-op override then costs nothing.
func sameTransformState(effective, cached *ResolvedStyle) bool {
	return styleValues(effective) == styleValues(cached)
}

// subtreeStamp is the work of one outermost override subtree.
type subtreeStamp struct {
	box     *box
	window  OpWindow
	state   stampState
	actions []ancestorStamp
}

// ancestorStamp is one ancestor stamp the original walk applied after the
// subtree's own stamp. The restamp replays it so an enclosing 3D projection or
// flat-boundary composition keeps wrapping the new op matrices.
type ancestorStamp struct {
	box  *box
	step stampBoxStep
}

// stampStateAt returns the state the full walk had when it reached target, and
// the ancestor stamps it applied after target's subtree.
func stampStateAt(root, target *box) (stampState, []ancestorStamp, bool) {
	if root == nil || target == nil {
		return stampState{}, nil, false //nolint:exhaustruct // no state
	}

	var actions []ancestorStamp

	var state stampState

	found := false

	var walk func(boxNode *box, current stampState) bool
	walk = func(boxNode *box, current stampState) bool {
		if boxNode == target {
			state = current
			found = true

			return true
		}

		step := advanceStamp(boxNode, current)
		actions = append(actions, ancestorStamp{box: boxNode, step: step})

		for _, child := range boxNode.children {
			if walk(child, step.child) {
				return true
			}
		}

		actions = actions[:len(actions)-1]

		return false
	}

	if !walk(root, initialStampState(IdentityMatrix())) {
		return stampState{}, nil, false //nolint:exhaustruct // no state
	}

	slices.Reverse(actions)

	return state, actions, found
}

// applyAncestorStamp replays one ancestor stamp over a restamped window.
func applyAncestorStamp(action ancestorStamp, ops []Op, window OpWindow) {
	if action.step.preservePath {
		if action.step.chainActive && !action.step.isPreserve {
			stampPreserveRangeWindow(
				action.box, action.step.worldChain, action.step.parentAccum, ops,
				window.Start, window.End, action.step.viewport,
			)
		}

		return
	}

	projective, ok := projectiveStampForBox(action.box, action.step.parentAccum, action.step.viewport)
	if !ok {
		return
	}

	for idx := window.Start; idx <= window.End; idx++ {
		if ops[idx].Kind == OpKindNoop {
			continue
		}

		ops[idx].setProjectiveTransform(projective)
	}
}

// resetStampFields drops the computed state a previous stamp left on one
// subtree. preserveComposed is derived from the chain above the box and the
// perspective fields from the nearest perspective ancestor, so both are
// recomputed rather than adjusted.
func resetStampFields(root *box) {
	if root == nil {
		return
	}

	root.preserveComposed = false

	for _, child := range root.children {
		resetStampFields(child)
	}
}

// nearestPerspectiveAncestor returns the nearest box above target that carries
// a perspective, or nil. The subtree pre-pass seeds from it, so an override to
// none falls back to the outer camera instead of inventing an orthographic one.
func nearestPerspectiveAncestor(root, target *box) *box {
	var found *box

	var walk func(boxNode *box, nearest *box) bool
	walk = func(boxNode *box, nearest *box) bool {
		if boxNode == target {
			found = nearest

			return true
		}

		next := nearest
		if boxNode.style != nil && boxNode.style.HasPerspective {
			next = boxNode
		}

		for _, child := range boxNode.children {
			if walk(child, next) {
				return true
			}
		}

		return false
	}

	if root == nil || !walk(root, nil) {
		return nil
	}

	return found
}

// opWindowFor returns the operation range a box's subtree emitted.
func opWindowFor(boxNode *box, opCount int) (OpWindow, bool) {
	if boxNode == nil || !boxOwnsOps(boxNode) {
		return OpWindow{}, false //nolint:exhaustruct // no range
	}

	start := boxNode.opStart
	end := min(boxNode.opEnd, opCount-1)

	if start < 0 || start > end {
		return OpWindow{}, false //nolint:exhaustruct // no range
	}

	return OpWindow{Start: start, End: end}, true
}

// resetWindowTransforms restores every operation in a window to the geometry
// it carried before finalize baked a projection into it, then drops the
// transform the previous stamp applied. The restamp walk rebuilds both.
func resetWindowTransforms(ops []Op, window OpWindow) {
	for idx := window.Start; idx <= window.End; idx++ {
		restorePristineGeometry(&ops[idx])
		ops[idx].XformSet = false
		ops[idx].clearProjectiveTransform()

		// setXform always detaches before writing, so every op that ever
		// carried a transform owns its extra. An op still pointing at the
		// shared zero singleton never had one, and the singleton is the
		// no-transform value by definition. Skip it: writing the shared
		// Xform would race with any other display reading Transform(), and
		// detaching would allocate for every op the restamp never touched.
		if extra := ops[idx].opExtra; extra != nil && extra != emptyExtra {
			extra.Xform = IdentityMatrix()
		}
	}
}

// savePristineGeometry records the operation before FinalizeDisplayOps rewrites
// it into final geometry, so a restamp can project it again from its flat form.
// The copy detaches the rare payload and clones the grid segments, which the
// finalize pass rewrites in place.
func (op *Op) savePristineGeometry() {
	if op == nil {
		return
	}

	extra := op.detachExtra()
	saved := op.Clone()
	saved.Grid = cloneGridRun(op.Grid)

	if saved.opExtra != nil {
		saved.opExtra.Pristine = nil
	}

	extra.Pristine = &saved
}

// restorePristineGeometry puts the pre-finalize form of one operation back.
// It reports whether a saved form existed.
func restorePristineGeometry(op *Op) bool {
	if op == nil || op.opExtra == nil || op.opExtra.Pristine == nil {
		return false
	}

	*op = *op.opExtra.Pristine

	return true
}

// cloneGridRun copies a collapsed border run so the saved operation cannot see
// the projected segments finalize writes in place.
func cloneGridRun(run *GridRun) *GridRun {
	if run == nil {
		return nil
	}

	return &GridRun{Segs: slices.Clone(run.Segs)}
}

// opBackup is one window's operations before the pass, for rollback.
type opBackup struct {
	start int
	ops   []Op
}

// backupWindow copies a window's operations so a failed pass can put them
// back. The clone detaches each rare payload, so restoring cannot alias state
// the failed pass already mutated.
func backupWindow(ops []Op, window OpWindow) opBackup {
	backup := opBackup{start: window.Start, ops: make([]Op, window.End-window.Start+1)}

	for idx := range backup.ops {
		backup.ops[idx] = ops[window.Start+idx].Clone()
	}

	return backup
}

// bounds is an axis-aligned box in layout points.
type bounds struct {
	x, y, w, h float64
	set        bool
}

// unionWindowBounds returns the painted area of every patched window.
func unionWindowBounds(ops []Op, windows []OpWindow) bounds {
	var out bounds

	for idx := range windows {
		window := windows[idx]

		for opIdx := window.Start; opIdx <= window.End; opIdx++ {
			op := &ops[opIdx]
			if op.Kind == OpKindNoop {
				continue
			}

			out.union(op.X, op.Y, op.W, op.H)
		}
	}

	return out
}

// union grows b to cover one rectangle.
func (b *bounds) union(left, top, width, height float64) {
	if width < 0 {
		left += width
		width = -width
	}

	if height < 0 {
		top += height
		height = -height
	}

	if !b.set {
		b.x, b.y, b.w, b.h, b.set = left, top, width, height, true

		return
	}

	right := math.Max(b.x+b.w, left+width)
	bottom := math.Max(b.y+b.h, top+height)
	b.x = math.Min(b.x, left)
	b.y = math.Min(b.y, top)
	b.w = right - b.x
	b.h = bottom - b.y
}
