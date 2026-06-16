package jsonx

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

func Normalize(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			n, err := Normalize(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = n
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			ks, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("unsupported map key type %T", k)
			}
			n, err := Normalize(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", ks, err)
			}
			out[ks] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			n, err := Normalize(v)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = n
		}
		return out, nil
	case string:
		return x, nil
	case bool:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("unsupported float value %v", x)
		}
		return x, nil
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("unsupported float value %v", x)
		}
		return f, nil
	case int:
		return float64(x), nil
	case int8:
		return float64(x), nil
	case int16:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case uint:
		return float64(x), nil
	case uint8:
		return float64(x), nil
	case uint16:
		return float64(x), nil
	case uint32:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid json number %q: %w", x, err)
		}
		return f, nil
	case time.Time:
		return x.Format(time.RFC3339Nano), nil
	default:
		return nil, fmt.Errorf("unsupported JSON value type %T", v)
	}
}

func NormalizeObject(v any) (map[string]any, error) {
	n, err := Normalize(v)
	if err != nil {
		return nil, err
	}
	obj, ok := n.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("document root must be an object, got %T", n)
	}
	return obj, nil
}

func Clone(v any) (any, error) {
	n, err := Normalize(v)
	if err != nil {
		return nil, err
	}
	switch x := n.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			c, err := Clone(v)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			c, err := Clone(v)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	default:
		return x, nil
	}
}
