package materialize

import (
	"strings"
	"testing"
)

func TestMarkdownFrontmatterRoundtrip(t *testing.T) {
	value, err := ParseFile("content/post.md", []byte("---\ntitle: Hello\ndraft: false\n---\nBody\n"))
	if err != nil {
		t.Fatal(err)
	}
	fm := value["frontmatter"].(map[string]any)
	if fm["title"] != "Hello" || fm["draft"] != false || value["body"] != "Body\n" {
		t.Fatalf("parsed = %#v", value)
	}
	body, err := RenderFile("content/post.md", value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "title: Hello") || !strings.HasSuffix(string(body), "Body\n") {
		t.Fatalf("rendered = %q", body)
	}
}

func TestMarkdownInvalidFrontmatterFails(t *testing.T) {
	if _, err := ParseFile("content/post.md", []byte("---\n- nope\n---\nBody\n")); err == nil {
		t.Fatal("expected non-object frontmatter error")
	}
}
