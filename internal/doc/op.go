package doc

import (
	"fmt"

	"github.com/herdworks/hypersync-go/internal/jsonx"
)

type Action string

const (
	ActionPut    Action = "put"
	ActionDelete Action = "delete"
)

type Op struct {
	ID        OpID     `json:"id"`
	DocID     string   `json:"doc_id"`
	Parents   []OpID   `json:"parents"`
	Action    Action   `json:"action"`
	Path      []string `json:"path"`
	Value     any      `json:"value"`
	Lamport   uint64   `json:"lamport"`
	Timestamp int64    `json:"timestamp,omitempty"`
}

type Conflict struct {
	Path   []string `json:"path"`
	Winner OpID     `json:"winner"`
	Loser  OpID     `json:"loser"`
	Value  any      `json:"value,omitempty"`
	Action Action   `json:"action"`
}

type Document struct {
	ID        string         `json:"id"`
	Heads     []OpID         `json:"heads"`
	Ops       map[string]Op  `json:"ops,omitempty"`
	Value     map[string]any `json:"value"`
	Conflicts []Conflict     `json:"conflicts,omitempty"`
}

func (op Op) Validate() error {
	if op.DocID == "" {
		return fmt.Errorf("doc_id is required")
	}
	if op.ID.Actor == "" {
		return fmt.Errorf("op actor is required")
	}
	if op.ID.Seq == 0 {
		return fmt.Errorf("op sequence must be greater than zero")
	}
	if op.Lamport == 0 {
		return fmt.Errorf("op lamport must be greater than zero")
	}
	switch op.Action {
	case ActionPut:
		if _, err := jsonx.Normalize(op.Value); err != nil {
			return fmt.Errorf("invalid put value: %w", err)
		}
	case ActionDelete:
	default:
		return fmt.Errorf("unknown action %q", op.Action)
	}
	if err := jsonx.ValidatePath(op.Path); err != nil {
		return err
	}
	seenParents := make(map[string]bool, len(op.Parents))
	for _, parent := range op.Parents {
		if parent.Actor == "" || parent.Seq == 0 {
			return fmt.Errorf("invalid parent id %q", parent.String())
		}
		if parent.String() == op.ID.String() {
			return fmt.Errorf("operation cannot list itself as a parent")
		}
		if seenParents[parent.String()] {
			return fmt.Errorf("duplicate parent %q", parent.String())
		}
		seenParents[parent.String()] = true
	}
	return nil
}

func CloneOp(op Op) (Op, error) {
	out := op
	out.Parents = append([]OpID(nil), op.Parents...)
	out.Path = jsonx.ClonePath(op.Path)
	if op.Action == ActionPut {
		v, err := jsonx.Clone(op.Value)
		if err != nil {
			return Op{}, err
		}
		out.Value = v
	}
	return out, nil
}
