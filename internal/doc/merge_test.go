package doc

import "testing"

func TestConcurrentDifferentPropertiesMerge(t *testing.T) {
	d := applyTestOps(t,
		testOp("a", 1, nil, ActionPut, []string{"frontmatter", "title"}, "New", 1),
		testOp("b", 1, nil, ActionPut, []string{"frontmatter", "draft"}, false, 1),
	)
	fm := d.Value["frontmatter"].(map[string]any)
	if fm["title"] != "New" || fm["draft"] != false {
		t.Fatalf("merged frontmatter = %#v", fm)
	}
	if len(d.Conflicts) != 0 {
		t.Fatalf("conflicts = %#v", d.Conflicts)
	}
}

func TestConcurrentSamePropertyConflictUsesLamportActor(t *testing.T) {
	d := applyTestOps(t,
		testOp("a", 1, nil, ActionPut, []string{"title"}, "Hello", 1),
		testOp("b", 1, nil, ActionPut, []string{"title"}, "Bonjour", 2),
	)
	if d.Value["title"] != "Bonjour" {
		t.Fatalf("title = %v", d.Value["title"])
	}
	if len(d.Conflicts) != 1 {
		t.Fatalf("conflicts len = %d, want 1", len(d.Conflicts))
	}
	if d.Conflicts[0].Loser.String() != "a:1" || d.Conflicts[0].Value != "Hello" {
		t.Fatalf("conflict = %#v", d.Conflicts[0])
	}
}

func TestCausalOverwriteNoConflict(t *testing.T) {
	d := applyTestOps(t,
		testOp("a", 1, nil, ActionPut, []string{"title"}, "Draft", 1),
		testOp("a", 2, []OpID{{Actor: "a", Seq: 1}}, ActionPut, []string{"title"}, "Final", 2),
	)
	if d.Value["title"] != "Final" {
		t.Fatalf("title = %v", d.Value["title"])
	}
	if len(d.Conflicts) != 0 {
		t.Fatalf("conflicts = %#v", d.Conflicts)
	}
}

func TestConcurrentPutBeatsDelete(t *testing.T) {
	d := applyTestOps(t,
		testOp("a", 1, nil, ActionPut, []string{"title"}, "Keep", 1),
		testOp("b", 1, nil, ActionDelete, []string{"title"}, nil, 9),
	)
	if d.Value["title"] != "Keep" {
		t.Fatalf("title = %v", d.Value["title"])
	}
	if len(d.Conflicts) != 1 || d.Conflicts[0].Action != ActionDelete {
		t.Fatalf("conflicts = %#v", d.Conflicts)
	}
}

func TestConcurrentArrayReplacementConflict(t *testing.T) {
	d := applyTestOps(t,
		testOp("a", 1, nil, ActionPut, []string{"tags"}, []any{"a"}, 1),
		testOp("b", 1, nil, ActionPut, []string{"tags"}, []any{"b"}, 2),
	)
	got := d.Value["tags"].([]any)
	if got[0] != "b" {
		t.Fatalf("tags = %#v", got)
	}
	if len(d.Conflicts) != 1 {
		t.Fatalf("conflicts = %#v", d.Conflicts)
	}
}

func applyTestOps(t *testing.T, ops ...Op) Document {
	t.Helper()
	engine := NewEngine()
	d, err := engine.Apply(Document{ID: "doc", Ops: map[string]Op{}, Value: map[string]any{}}, ops)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func testOp(actor string, seq uint64, parents []OpID, action Action, path []string, value any, lamport uint64) Op {
	return Op{
		ID:      OpID{Actor: actor, Seq: seq},
		DocID:   "doc",
		Parents: parents,
		Action:  action,
		Path:    path,
		Value:   value,
		Lamport: lamport,
	}
}
