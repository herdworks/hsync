package doc

import "testing"

func TestRecomputeHeads(t *testing.T) {
	ops := map[string]Op{
		"a:1": testOp("a", 1, nil, ActionPut, []string{"title"}, "one", 1),
		"a:2": testOp("a", 2, []OpID{{Actor: "a", Seq: 1}}, ActionPut, []string{"title"}, "two", 2),
		"b:1": testOp("b", 1, nil, ActionPut, []string{"draft"}, false, 1),
	}
	heads := RecomputeHeads(ops)
	if len(heads) != 2 {
		t.Fatalf("heads len = %d, want 2: %#v", len(heads), heads)
	}
	if heads[0].String() != "b:1" || heads[1].String() != "a:2" {
		t.Fatalf("heads = %v", heads)
	}
	if !IsAncestor(ops, OpID{Actor: "a", Seq: 1}, OpID{Actor: "a", Seq: 2}) {
		t.Fatal("a:1 should be ancestor of a:2")
	}
	if !AreConcurrent(ops, OpID{Actor: "a", Seq: 2}, OpID{Actor: "b", Seq: 1}) {
		t.Fatal("a:2 and b:1 should be concurrent")
	}
}
