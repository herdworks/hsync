package classify

import "testing"

func TestClassifyDefaults(t *testing.T) {
	cases := map[string]Bucket{
		".sync/state":         BucketIgnored,
		".hypersync/site":     BucketIgnored,
		"node_modules/x.js":   BucketIgnored,
		"config.yaml":         BucketConfig,
		"hugo.toml":           BucketConfig,
		"content/post.md":     BucketContent,
		"data/items.json":     BucketData,
		"plugins/foo/main.js": BucketPlugins,
		"themes/base/a.css":   BucketThemes,
		"assets/logo.png":     BucketStatic,
	}
	for path, want := range cases {
		got := Classify(path).Bucket
		if got != want {
			t.Fatalf("Classify(%q) = %q, want %q", path, got, want)
		}
	}
}
