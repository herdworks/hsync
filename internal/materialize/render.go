package materialize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/herdworks/hypersync-go/internal/jsonx"
	"gopkg.in/yaml.v3"
)

func ParseFile(rel string, body []byte) (map[string]any, error) {
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".json":
		var v any
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("parse JSON %s: %w", rel, err)
		}
		obj, err := jsonx.NormalizeObject(v)
		if err != nil {
			return nil, fmt.Errorf("parse JSON %s: %w", rel, err)
		}
		return obj, nil
	case ".yaml", ".yml":
		var v any
		if err := yaml.Unmarshal(body, &v); err != nil {
			return nil, fmt.Errorf("parse YAML %s: %w", rel, err)
		}
		obj, err := jsonx.NormalizeObject(v)
		if err != nil {
			return nil, fmt.Errorf("parse YAML %s: %w", rel, err)
		}
		return obj, nil
	case ".md", ".markdown":
		return parseMarkdown(rel, string(body))
	default:
		return map[string]any{"body": string(body)}, nil
	}
}

func RenderFile(rel string, value map[string]any) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".json":
		b, err := jsonx.Pretty(value)
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	case ".yaml", ".yml":
		b, err := yaml.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("render YAML %s: %w", rel, err)
		}
		return b, nil
	case ".md", ".markdown":
		return renderMarkdown(rel, value)
	default:
		body, ok := value["body"].(string)
		if !ok {
			return nil, fmt.Errorf("render %s: opaque document requires string body", rel)
		}
		return []byte(body), nil
	}
}

func parseMarkdown(rel, text string) (map[string]any, error) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return map[string]any{"body": text}, nil
	}
	newline := "\n"
	startLen := len("---\n")
	if strings.HasPrefix(text, "---\r\n") {
		newline = "\r\n"
		startLen = len("---\r\n")
	}
	rest := text[startLen:]
	marker := newline + "---" + newline
	idx := strings.Index(rest, marker)
	if idx < 0 {
		return nil, fmt.Errorf("parse markdown %s: missing closing frontmatter delimiter", rel)
	}
	fmText := rest[:idx]
	body := rest[idx+len(marker):]
	var fm any
	if strings.TrimSpace(fmText) == "" {
		fm = map[string]any{}
	} else if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return nil, fmt.Errorf("parse markdown frontmatter %s: %w", rel, err)
	}
	frontmatter, err := jsonx.NormalizeObject(fm)
	if err != nil {
		return nil, fmt.Errorf("parse markdown frontmatter %s: %w", rel, err)
	}
	return map[string]any{"frontmatter": frontmatter, "body": body}, nil
}

func renderMarkdown(rel string, value map[string]any) ([]byte, error) {
	body, ok := value["body"].(string)
	if !ok {
		return nil, fmt.Errorf("render markdown %s: body must be a string", rel)
	}
	fm, hasFM := value["frontmatter"]
	if !hasFM {
		return []byte(body), nil
	}
	fmObj, ok := fm.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("render markdown %s: frontmatter must be an object", rel)
	}
	fmBytes, err := yaml.Marshal(fmObj)
	if err != nil {
		return nil, fmt.Errorf("render markdown frontmatter %s: %w", rel, err)
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(fmBytes)
	out.WriteString("---\n")
	out.WriteString(body)
	return out.Bytes(), nil
}
