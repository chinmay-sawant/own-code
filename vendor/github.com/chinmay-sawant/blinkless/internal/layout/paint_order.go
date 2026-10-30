package layout

import "sort"

const (
	paintLayerChrome = iota
	paintLayerContent
	paintLayerOutline
)

// PaintOrder returns operation indices in the canonical display-list paint
// order. Display-list consumers share this same policy; backend drawing
// remains responsible for interpreting each operation.
func PaintOrder(ops []Op) []int {
	idx := make([]int, len(ops))
	for i := range ops {
		idx[i] = i
	}

	sortPaintIndices(ops, idx)

	return idx
}

// paintOrderSubset returns an ordered copy of an operation subset. All
// adapters use this policy so a band cannot accidentally fall back to source
// order while the paginated body uses paint order.
func paintOrderSubset(ops []Op, idxs []int) []int {
	ordered := append([]int(nil), idxs...)
	sortPaintIndices(ops, ordered)

	return ordered
}

// sortPaintIndices sorts an existing operation subset without changing which
// operations it contains. Pagination tests use this compatibility helper for
// fixed and per-page lists; the comparison itself lives in paintOrderBefore.
func sortPaintIndices(ops []Op, idxs []int) {
	if hasGroups(ops, idxs) {
		ordered := groupedPaintOrder(ops, idxs)
		copy(idxs, ordered)

		return
	}

	sort.SliceStable(idxs, func(i, j int) bool {
		return paintOrderBefore(ops, idxs[i], idxs[j])
	})
}

func hasGroups(ops []Op, idxs []int) bool {
	for _, idx := range idxs {
		if ops[idx].Group() != nil {
			return true
		}
	}

	return false
}

type paintGroupOrder struct {
	group *BlendGroup
	// anchor uses the group's begin marker from the full op list, even when a
	// subset omits that marker. It is the group's slot in its parent's order.
	anchor int
	// begins and ends contain only marker indices selected by the caller.
	begins   []int
	ends     []int
	ops      []int
	children []*paintGroupOrder
}

type paintOrderItem struct {
	op    int
	group *paintGroupOrder
}

type paintOrderBuilder struct {
	anchors map[*BlendGroup]int
	groups  map[*BlendGroup]*paintGroupOrder
	roots   []*paintGroupOrder
}

// groupedPaintOrder keeps every group as one item in its parent's paint
// order. Group membership comes from the operation's group pointer and the
// group's Parent chain. Boundary markers are tokens to emit, not source-index
// ranges: inline paint can belong to a group after its end marker in Ops.
func groupedPaintOrder(ops []Op, idxs []int) []int {
	builder := newPaintOrderBuilder(ops)
	rootOps := builder.collect(ops, idxs)

	items := make([]paintOrderItem, 0, len(rootOps)+len(builder.roots))
	for _, idx := range rootOps {
		items = append(items, paintOrderItem{op: idx, group: nil})
	}

	for _, group := range builder.roots {
		items = append(items, paintOrderItem{op: 0, group: group})
	}

	out := make([]int, 0, len(idxs))
	appendSortedPaintItems(ops, items, &out)

	return out
}

func newPaintOrderBuilder(ops []Op) *paintOrderBuilder {
	builder := &paintOrderBuilder{
		anchors: paintGroupAnchors(ops),
		groups:  make(map[*BlendGroup]*paintGroupOrder),
		roots:   nil,
	}

	return builder
}

func paintGroupAnchors(ops []Op) map[*BlendGroup]int {
	firstMember := make(map[*BlendGroup]int)
	firstBegin := make(map[*BlendGroup]int)

	// Use the full list to keep a group's owner slot when idxs omits its
	// boundary markers, as page and band subsets may do.
	for idx := range ops {
		operation := &ops[idx]
		group := operation.Group()

		for owner := group; owner != nil; owner = owner.Parent {
			if _, ok := firstMember[owner]; !ok {
				firstMember[owner] = idx
			}
		}

		if group != nil && operation.IsGroupBegin() {
			if _, ok := firstBegin[group]; !ok {
				firstBegin[group] = idx
			}
		}
	}

	anchors := make(map[*BlendGroup]int, len(firstMember)+len(firstBegin))
	for group, idx := range firstMember {
		anchors[group] = idx
	}

	for group, idx := range firstBegin {
		anchors[group] = idx
	}

	return anchors
}

func (builder *paintOrderBuilder) collect(ops []Op, idxs []int) []int {
	rootOps := make([]int, 0, len(idxs))

	for _, idx := range idxs {
		operation := &ops[idx]
		group := operation.Group()

		if group == nil {
			rootOps = append(rootOps, idx)

			continue
		}

		builder.collectGroupOperation(group, operation.GroupBoundary(), idx)
	}

	return rootOps
}

func (builder *paintOrderBuilder) ensureGroup(group *BlendGroup) *paintGroupOrder {
	if group == nil {
		return nil
	}

	if order, ok := builder.groups[group]; ok {
		return order
	}

	anchor, ok := builder.anchors[group]
	if !ok {
		anchor = -1
	}

	order := &paintGroupOrder{
		group:    group,
		anchor:   anchor,
		begins:   nil,
		ends:     nil,
		ops:      nil,
		children: nil,
	}
	builder.groups[group] = order

	parent := builder.ensureGroup(group.Parent)
	if parent == nil {
		builder.roots = append(builder.roots, order)
	} else {
		parent.children = append(parent.children, order)
	}

	return order
}

func (builder *paintOrderBuilder) collectGroupOperation(
	group *BlendGroup, boundary uint8, opIndex int,
) {
	order := builder.ensureGroup(group)

	switch boundary {
	case groupMarkBegin:
		order.begins = append(order.begins, opIndex)
	case groupMarkEnd:
		order.ends = append(order.ends, opIndex)
	default:
		order.ops = append(order.ops, opIndex)
	}
}

func appendSortedPaintItems(ops []Op, items []paintOrderItem, out *[]int) {
	itemIndex := func(item paintOrderItem) int {
		if item.group == nil {
			return item.op
		}

		return item.group.anchor
	}

	sort.SliceStable(items, func(i, j int) bool {
		return paintOrderBefore(ops, itemIndex(items[i]), itemIndex(items[j]))
	})

	for _, item := range items {
		if item.group == nil {
			*out = append(*out, item.op)

			continue
		}

		*out = append(*out, item.group.begins...)

		children := make([]paintOrderItem, 0, len(item.group.ops)+len(item.group.children))
		for _, idx := range item.group.ops {
			children = append(children, paintOrderItem{op: idx, group: nil})
		}

		for _, child := range item.group.children {
			children = append(children, paintOrderItem{op: 0, group: child})
		}

		appendSortedPaintItems(ops, children, out)

		*out = append(*out, item.group.ends...)
	}
}

func paintOrderBefore(ops []Op, left, right int) bool {
	leftOp, rightOp := &ops[left], &ops[right]

	leftZ, rightZ := 0, 0
	if leftOp.ZIndexSet {
		leftZ = leftOp.ZIndex
	}

	if rightOp.ZIndexSet {
		rightZ = rightOp.ZIndex
	}

	if leftZ != rightZ {
		return leftZ < rightZ
	}

	if leftOp.Positioned != rightOp.Positioned {
		return !leftOp.Positioned
	}

	leftLayer, rightLayer := paintLayer(leftOp), paintLayer(rightOp)
	if leftLayer != rightLayer {
		return leftLayer < rightLayer
	}

	return left < right
}

// paintLayer orders ops within a z-index band: ordinary chrome under content,
// outlines above descendant content.
func paintLayer(paintOp *Op) int {
	if paintOp.IsBackground {
		return paintLayerChrome
	}

	if paintOp.isOutline() {
		return paintLayerOutline
	}

	switch paintOp.Kind {
	case OpFillRect, OpStrokeRect, OpLine, OpGridRun, OpPath:
		return paintLayerChrome
	case OpText, OpImage, OpLinkURI, OpBullet, OpUnknown, opKindNoop:
		return paintLayerContent
	}

	return paintLayerContent
}
