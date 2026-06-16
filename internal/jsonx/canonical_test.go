package jsonx

import "testing"

func TestCanonicalSortsKeys(t *testing.T) {
	got, err := Canonical(map[string]any{"b": float64(2), "a": []any{"x", nil}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":["x",null],"b":2}`
	if string(got) != want {
		t.Fatalf("canonical = %s, want %s", got, want)
	}
}
