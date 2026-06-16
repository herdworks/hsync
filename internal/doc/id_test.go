package doc

import "testing"

func TestOpIDParseStringSort(t *testing.T) {
	id, err := ParseOpID("actor-b:12")
	if err != nil {
		t.Fatal(err)
	}
	if id.Actor != "actor-b" || id.Seq != 12 || id.String() != "actor-b:12" {
		t.Fatalf("unexpected id: %#v", id)
	}
	ids := []OpID{{Actor: "b", Seq: 2}, {Actor: "c", Seq: 1}, {Actor: "a", Seq: 1}}
	SortOpIDs(ids)
	got := []string{ids[0].String(), ids[1].String(), ids[2].String()}
	want := []string{"a:1", "c:1", "b:2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}
