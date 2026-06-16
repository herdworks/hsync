package classify

type Bucket string

const (
	BucketIgnored Bucket = "ignored"
	BucketConfig  Bucket = "config"
	BucketContent Bucket = "content"
	BucketData    Bucket = "data"
	BucketPlugins Bucket = "plugins"
	BucketThemes  Bucket = "themes"
	BucketStatic  Bucket = "static"
)

type Result struct {
	Path     string
	Bucket   Bucket
	Ignored  bool
	Warnings []string
}
