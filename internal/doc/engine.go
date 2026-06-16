package doc

import (
	"errors"
	"fmt"
)

var ErrNonObjectRoot = errors.New("document root must remain an object")

type Engine interface {
	Apply(base Document, ops []Op) (Document, error)
	Materialize(doc Document) (map[string]any, []Conflict, error)
}

type DefaultEngine struct{}

func NewEngine() DefaultEngine {
	return DefaultEngine{}
}

func (DefaultEngine) Apply(base Document, newOps []Op) (Document, error) {
	if base.ID == "" {
		return Document{}, fmt.Errorf("document id is required")
	}
	all := make(map[string]Op)
	for key, op := range base.Ops {
		if key == "" {
			key = op.ID.String()
		}
		cloned, err := CloneOp(op)
		if err != nil {
			return Document{}, fmt.Errorf("clone base op %s: %w", op.ID.String(), err)
		}
		if err := cloned.Validate(); err != nil {
			return Document{}, fmt.Errorf("validate base op %s: %w", op.ID.String(), err)
		}
		all[key] = cloned
	}
	for _, op := range newOps {
		if op.DocID != base.ID {
			return Document{}, fmt.Errorf("op %s belongs to doc %q, not %q", op.ID.String(), op.DocID, base.ID)
		}
		cloned, err := CloneOp(op)
		if err != nil {
			return Document{}, fmt.Errorf("clone op %s: %w", op.ID.String(), err)
		}
		if err := cloned.Validate(); err != nil {
			return Document{}, fmt.Errorf("validate op %s: %w", op.ID.String(), err)
		}
		key := cloned.ID.String()
		if existing, ok := all[key]; ok {
			same, err := SameOp(existing, cloned)
			if err != nil {
				return Document{}, err
			}
			if !same {
				return Document{}, fmt.Errorf("duplicate op id %s with different content", key)
			}
			continue
		}
		all[key] = cloned
	}
	if _, err := TopologicalSort(all); err != nil {
		return Document{}, err
	}
	value, conflicts, err := materializeOps(base.ID, all)
	if err != nil {
		return Document{}, err
	}
	return Document{
		ID:        base.ID,
		Heads:     RecomputeHeads(all),
		Ops:       all,
		Value:     value,
		Conflicts: conflicts,
	}, nil
}

func (DefaultEngine) Materialize(doc Document) (map[string]any, []Conflict, error) {
	value, conflicts, err := materializeOps(doc.ID, doc.Ops)
	if err != nil {
		return nil, nil, err
	}
	return value, conflicts, nil
}
