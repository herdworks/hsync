package doc

import (
	"sort"

	"github.com/herdworks/hypersync-go/internal/jsonx"
)

type Change struct {
	Action Action
	Path   []string
	Value  any
}

func DiffValues(oldValue, newValue map[string]any) ([]Change, error) {
	oldNorm, err := jsonx.NormalizeObject(oldValue)
	if err != nil {
		return nil, err
	}
	newNorm, err := jsonx.NormalizeObject(newValue)
	if err != nil {
		return nil, err
	}
	return diffObject(nil, oldNorm, newNorm)
}

func diffObject(prefix []string, oldObj, newObj map[string]any) ([]Change, error) {
	keys := map[string]bool{}
	for k := range oldObj {
		keys[k] = true
	}
	for k := range newObj {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	var changes []Change
	for _, key := range ordered {
		path := append(append([]string(nil), prefix...), key)
		oldVal, oldOK := oldObj[key]
		newVal, newOK := newObj[key]
		switch {
		case !oldOK:
			changes = append(changes, Change{Action: ActionPut, Path: path, Value: newVal})
		case !newOK:
			changes = append(changes, Change{Action: ActionDelete, Path: path})
		default:
			oldChild, oldIsObj := oldVal.(map[string]any)
			newChild, newIsObj := newVal.(map[string]any)
			if oldIsObj && newIsObj {
				nested, err := diffObject(path, oldChild, newChild)
				if err != nil {
					return nil, err
				}
				changes = append(changes, nested...)
				continue
			}
			equal, err := jsonx.Equal(oldVal, newVal)
			if err != nil {
				return nil, err
			}
			if !equal {
				changes = append(changes, Change{Action: ActionPut, Path: path, Value: newVal})
			}
		}
	}
	return changes, nil
}
