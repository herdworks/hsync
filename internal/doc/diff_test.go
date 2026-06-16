package doc

import "testing"

func TestDiffValues(t *testing.T) {
	oldValue := map[string]any{
		"title": "Old",
		"meta":  map[string]any{"draft": true},
		"tags":  []any{"a", "b"},
		"gone":  "yes",
	}
	newValue := map[string]any{
		"title": "New",
		"meta":  map[string]any{"draft": false},
		"tags":  []any{"a", "c"},
		"added": "ok",
	}
	changes, err := DiffValues(oldValue, newValue)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]Action)
	for _, change := range changes {
		got[pathKey(change.Path)] = change.Action
	}
	want := map[string]Action{
		"added":         ActionPut,
		"gone":          ActionDelete,
		"meta\x00draft": ActionPut,
		"tags":          ActionPut,
		"title":         ActionPut,
	}
	for path, action := range want {
		if got[path] != action {
			t.Fatalf("change %q = %q, want %q; all=%#v", path, got[path], action, changes)
		}
	}
}
