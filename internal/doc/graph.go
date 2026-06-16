package doc

import (
	"fmt"
	"sort"
)

func IsAncestor(ops map[string]Op, ancestor OpID, descendant OpID) bool {
	if ancestor.String() == descendant.String() {
		return true
	}
	seen := map[string]bool{}
	var walk func(OpID) bool
	walk = func(id OpID) bool {
		key := id.String()
		if seen[key] {
			return false
		}
		seen[key] = true
		op, ok := ops[key]
		if !ok {
			return false
		}
		for _, parent := range op.Parents {
			if parent.String() == ancestor.String() || walk(parent) {
				return true
			}
		}
		return false
	}
	return walk(descendant)
}

func AreConcurrent(ops map[string]Op, a, b OpID) bool {
	return !IsAncestor(ops, a, b) && !IsAncestor(ops, b, a)
}

func RecomputeHeads(ops map[string]Op) []OpID {
	hasChild := make(map[string]bool)
	for _, op := range ops {
		for _, parent := range op.Parents {
			if _, ok := ops[parent.String()]; ok {
				hasChild[parent.String()] = true
			}
		}
	}
	heads := make([]OpID, 0)
	for key, op := range ops {
		if !hasChild[key] {
			heads = append(heads, op.ID)
		}
	}
	SortOpIDs(heads)
	return heads
}

func ParentLamports(ops map[string]Op, parents []OpID) ([]uint64, error) {
	out := make([]uint64, 0, len(parents))
	for _, parent := range parents {
		op, ok := ops[parent.String()]
		if !ok {
			return nil, fmt.Errorf("missing parent %s", parent.String())
		}
		out = append(out, op.Lamport)
	}
	return out, nil
}

func compareLamportOp(a, b Op) int {
	if a.Lamport < b.Lamport {
		return -1
	}
	if a.Lamport > b.Lamport {
		return 1
	}
	if a.ID.Actor < b.ID.Actor {
		return -1
	}
	if a.ID.Actor > b.ID.Actor {
		return 1
	}
	return CompareOpID(a.ID, b.ID)
}

func sortOpsStable(ops []Op) {
	sort.Slice(ops, func(i, j int) bool {
		return compareLamportOp(ops[i], ops[j]) < 0
	})
}

func TopologicalSort(ops map[string]Op) ([]Op, error) {
	indegree := make(map[string]int, len(ops))
	children := make(map[string][]string)
	for key := range ops {
		indegree[key] = 0
	}
	for key, op := range ops {
		for _, parent := range op.Parents {
			pkey := parent.String()
			if _, ok := ops[pkey]; !ok {
				return nil, fmt.Errorf("missing parent %s for op %s", pkey, key)
			}
			indegree[key]++
			children[pkey] = append(children[pkey], key)
		}
	}
	ready := make([]Op, 0)
	for key, n := range indegree {
		if n == 0 {
			ready = append(ready, ops[key])
		}
	}
	sortOpsStable(ready)
	out := make([]Op, 0, len(ops))
	for len(ready) > 0 {
		op := ready[0]
		ready = ready[1:]
		out = append(out, op)
		for _, childKey := range children[op.ID.String()] {
			indegree[childKey]--
			if indegree[childKey] == 0 {
				ready = append(ready, ops[childKey])
			}
		}
		sortOpsStable(ready)
	}
	if len(out) != len(ops) {
		return nil, fmt.Errorf("operation graph contains a cycle")
	}
	return out, nil
}
