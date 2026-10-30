package layout

import "github.com/chinmay-sawant/blinkless/internal/html"

// OperationOwners returns the nearest common element owner of each operation.
// Shared operations whose box ranges cross sibling elements belong to their
// nearest common element ancestor.
func OperationOwners(res *Result) []int {
	if res == nil {
		return nil
	}

	owners := make([]int, len(res.Ops))
	for index := range owners {
		owners[index] = -1
	}

	placed := PlacedElements(res)
	placedIndex := 0
	walkOperationOwners(res.root, -1, &placedIndex, owners, placed)

	return owners
}

func walkOperationOwners(boxNode *box, parent int, placedIndex *int, owners []int, placed []PlacedElement) {
	if boxNode == nil {
		return
	}

	owner := parent
	if boxNode.node != nil && boxNode.node.Type == html.ElementNode {
		owner = *placedIndex
		(*placedIndex)++

		if owner >= 0 && boxNode.opStart >= 0 && boxNode.opEnd >= boxNode.opStart {
			for index := boxNode.opStart; index <= boxNode.opEnd && index < len(owners); index++ {
				owners[index] = closestOwner(owners[index], owner, placed)
			}
		}
	}

	for _, child := range boxNode.children {
		walkOperationOwners(child, owner, placedIndex, owners, placed)
	}
}

func closestOwner(current, candidate int, placed []PlacedElement) int {
	if current < 0 {
		return candidate
	}

	if elementAncestor(current, candidate, placed) {
		return candidate
	}

	if elementAncestor(candidate, current, placed) {
		return current
	}

	for ancestor := placed[current].ParentIndex; ancestor >= 0; ancestor = placed[ancestor].ParentIndex {
		if elementAncestor(ancestor, candidate, placed) {
			return ancestor
		}
	}

	return -1
}

func elementAncestor(ancestor, child int, placed []PlacedElement) bool {
	for child >= 0 {
		if child == ancestor {
			return true
		}

		child = placed[child].ParentIndex
	}

	return false
}
