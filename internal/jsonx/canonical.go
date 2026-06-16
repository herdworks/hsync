package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

func Canonical(v any) ([]byte, error) {
	n, err := Normalize(v)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := writeCanonical(&b, n); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func Equal(a, b any) (bool, error) {
	ac, err := Canonical(a)
	if err != nil {
		return false, err
	}
	bc, err := Canonical(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ac, bc), nil
}

func Pretty(v any) ([]byte, error) {
	n, err := Normalize(v)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(n, "", "  ")
}

func writeCanonical(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case string:
		enc, _ := json.Marshal(x)
		b.Write(enc)
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		enc, err := json.Marshal(x)
		if err != nil {
			return err
		}
		b.Write(enc)
	case []any:
		b.WriteByte('[')
		for i, v := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, v); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			enc, _ := json.Marshal(k)
			b.Write(enc)
			b.WriteByte(':')
			if err := writeCanonical(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON value type %T", v)
	}
	return nil
}
