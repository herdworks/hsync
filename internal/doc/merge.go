package doc

import (
	"sort"
	"strings"

	"github.com/herdworks/hypersync-go/internal/jsonx"
)

type propertyState struct {
	path      []string
	winner    Op
	conflicts []Conflict
}

func materializeOps(docID string, ops map[string]Op) (map[string]any, []Conflict, error) {
	states := map[string]propertyState{}
	paths := map[string][]Op{}
	for _, op := range ops {
		if op.DocID != docID {
			continue
		}
		key := pathKey(op.Path)
		paths[key] = append(paths[key], op)
	}
	for key, pathOps := range paths {
		active := propertyActiveOps(ops, pathOps)
		if len(active) == 0 {
			continue
		}
		winner := chooseWinner(active)
		state := propertyState{path: jsonx.ClonePath(winner.Path), winner: winner}
		for _, op := range active {
			if op.ID.String() == winner.ID.String() {
				continue
			}
			state.conflicts = append(state.conflicts, conflictFor(winner, op))
		}
		sort.Slice(state.conflicts, func(i, j int) bool {
			return CompareOpID(state.conflicts[i].Loser, state.conflicts[j].Loser) < 0
		})
		states[key] = state
	}
	keys := make([]string, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := states[keys[i]].path, states[keys[j]].path
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		return strings.Join(a, "\x00") < strings.Join(b, "\x00")
	})
	value := map[string]any{}
	conflicts := make([]Conflict, 0)
	for _, key := range keys {
		state := states[key]
		if state.winner.Action == ActionPut {
			if err := putPath(value, state.winner.Path, state.winner.Value); err != nil {
				return nil, nil, err
			}
		} else {
			deletePath(value, state.winner.Path)
		}
		conflicts = append(conflicts, state.conflicts...)
	}
	return value, conflicts, nil
}

func propertyActiveOps(all map[string]Op, ops []Op) []Op {
	active := make([]Op, 0, len(ops))
	for _, candidate := range ops {
		superseded := false
		for _, other := range ops {
			if candidate.ID.String() == other.ID.String() {
				continue
			}
			if IsAncestor(all, candidate.ID, other.ID) {
				superseded = true
				break
			}
		}
		if !superseded {
			active = append(active, candidate)
		}
	}
	sortOpsStable(active)
	return active
}

func chooseWinner(ops []Op) Op {
	var winner Op
	have := false
	for _, op := range ops {
		if op.Action == ActionDelete {
			continue
		}
		if !have || compareLamportOp(winner, op) < 0 {
			winner = op
			have = true
		}
	}
	if have {
		return winner
	}
	for _, op := range ops {
		if !have || compareLamportOp(winner, op) < 0 {
			winner = op
			have = true
		}
	}
	return winner
}

func conflictFor(winner, loser Op) Conflict {
	return Conflict{
		Path:   jsonx.ClonePath(loser.Path),
		Winner: winner.ID,
		Loser:  loser.ID,
		Value:  loser.Value,
		Action: loser.Action,
	}
}

func putPath(root map[string]any, path []string, value any) error {
	if len(path) == 0 {
		obj, ok := value.(map[string]any)
		if !ok {
			return ErrNonObjectRoot
		}
		for k := range root {
			delete(root, k)
		}
		for k, v := range obj {
			root[k] = v
		}
		return nil
	}
	cur := root
	for _, part := range path[:len(path)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
	v, err := jsonx.Clone(value)
	if err != nil {
		return err
	}
	cur[path[len(path)-1]] = v
	return nil
}

func deletePath(root map[string]any, path []string) {
	if len(path) == 0 {
		for k := range root {
			delete(root, k)
		}
		return
	}
	cur := root
	for _, part := range path[:len(path)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, path[len(path)-1])
}

func pathKey(path []string) string {
	return strings.Join(path, "\x00")
}
