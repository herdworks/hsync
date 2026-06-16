package doc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/herdworks/hypersync-go/internal/jsonx"
)

func ReadJSONL(r io.Reader) ([]Op, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	seen := map[string][]byte{}
	ops := make([]Op, 0)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var op Op
		if err := json.Unmarshal(line, &op); err != nil {
			return nil, fmt.Errorf("line %d: malformed JSON: %w", lineNo, err)
		}
		if err := op.Validate(); err != nil {
			return nil, fmt.Errorf("line %d: invalid op: %w", lineNo, err)
		}
		canonical, err := canonicalOp(op)
		if err != nil {
			return nil, fmt.Errorf("line %d: canonicalize op: %w", lineNo, err)
		}
		key := op.ID.String()
		if prev, ok := seen[key]; ok {
			if !bytes.Equal(prev, canonical) {
				return nil, fmt.Errorf("line %d: duplicate op id %s with different content", lineNo, key)
			}
			continue
		}
		seen[key] = canonical
		ops = append(ops, op)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read JSONL: %w", err)
	}
	sort.Slice(ops, func(i, j int) bool {
		return CompareOpID(ops[i].ID, ops[j].ID) < 0
	})
	return ops, nil
}

func WriteJSONL(w io.Writer, ops []Op) error {
	out := append([]Op(nil), ops...)
	sort.Slice(out, func(i, j int) bool {
		return CompareOpID(out[i].ID, out[j].ID) < 0
	})
	enc := json.NewEncoder(w)
	for _, op := range out {
		if err := op.Validate(); err != nil {
			return err
		}
		if err := enc.Encode(op); err != nil {
			return err
		}
	}
	return nil
}

func SameOp(a, b Op) (bool, error) {
	ac, err := canonicalOp(a)
	if err != nil {
		return false, err
	}
	bc, err := canonicalOp(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ac, bc), nil
}

func canonicalOp(op Op) ([]byte, error) {
	b, err := json.Marshal(op)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return jsonx.Canonical(v)
}
