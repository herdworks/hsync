package doc

import (
	"strings"
	"testing"
)

func TestJSONLDedupAndRejectConflict(t *testing.T) {
	line := `{"id":{"actor":"a","seq":1},"doc_id":"doc","parents":[],"action":"put","path":["title"],"value":"Hello","lamport":1}`
	ops, err := ReadJSONL(strings.NewReader("\n" + line + "\n" + line + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops len = %d, want 1", len(ops))
	}
	other := `{"id":{"actor":"a","seq":1},"doc_id":"doc","parents":[],"action":"put","path":["title"],"value":"Bonjour","lamport":1}`
	if _, err := ReadJSONL(strings.NewReader(line + "\n" + other + "\n")); err == nil {
		t.Fatal("expected duplicate conflicting op error")
	}
}
