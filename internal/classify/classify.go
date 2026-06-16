package classify

import (
	"fmt"
	"path"
	"strings"
)

var ignoredNames = map[string]bool{
	".sync":        true,
	".hypersync":   true,
	".builds":      true,
	"builds":       true,
	".deployments": true,
	"deployments":  true,
	"node_modules": true,
}

var configNames = map[string]bool{
	"config.yaml": true,
	"config.json": true,
	"site.yaml":   true,
	"site.json":   true,
	"hugo.yaml":   true,
	"hugo.yml":    true,
	"hugo.toml":   true,
	"hugo.json":   true,
}

func Classify(rel string) Result {
	clean := strings.TrimPrefix(path.Clean(strings.ReplaceAll(rel, "\\", "/")), "./")
	if clean == "." {
		clean = ""
	}
	result := Result{Path: clean}
	if clean == "" {
		result.Ignored = true
		result.Bucket = BucketIgnored
		return result
	}
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		if ignoredNames[part] {
			result.Ignored = true
			result.Bucket = BucketIgnored
			return result
		}
	}
	matches := make([]Bucket, 0, 2)
	base := path.Base(clean)
	if len(parts) == 1 && configNames[base] {
		matches = append(matches, BucketConfig)
	}
	switch parts[0] {
	case "content":
		matches = append(matches, BucketContent)
	case "data":
		matches = append(matches, BucketData)
	case "plugins":
		matches = append(matches, BucketPlugins)
	case "themes":
		matches = append(matches, BucketThemes)
	}
	if len(matches) == 0 {
		result.Bucket = BucketStatic
		return result
	}
	if len(matches) > 1 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s matched multiple buckets: %v", clean, matches))
	}
	result.Bucket = matches[0]
	return result
}

func IsDocumentBucket(bucket Bucket) bool {
	switch bucket {
	case BucketConfig, BucketContent, BucketData, BucketPlugins, BucketThemes:
		return true
	default:
		return false
	}
}
