// Package frontmatter splits markdown files with YAML frontmatter delimited by "---".
package frontmatter

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

var delimiter = []byte("---")

// Parse splits data on "---" delimiters, unmarshals the YAML frontmatter into v,
// and returns the remaining markdown body. If no frontmatter is found the entire
// content is returned as body and v is left untouched.
func Parse(data []byte, v any) (body string, err error) {
	trimmed := bytes.TrimLeft(data, "\n")
	if !bytes.HasPrefix(trimmed, delimiter) {
		return string(data), nil
	}

	// Find the closing delimiter.
	rest := trimmed[len(delimiter):]
	rest = bytes.TrimPrefix(rest, []byte("\n"))
	idx := bytes.Index(rest, delimiter)
	if idx < 0 {
		return string(data), nil
	}

	front := rest[:idx]
	body = string(rest[idx+len(delimiter):])

	// Strip exactly one leading newline from the body.
	body = strings.TrimPrefix(body, "\n")

	if len(bytes.TrimSpace(front)) > 0 {
		if err := yaml.Unmarshal(front, v); err != nil {
			return "", err
		}
	}
	return body, nil
}

// Render marshals v to YAML, wraps it in "---" delimiters, and appends body.
func Render(v any, body string) ([]byte, error) {
	yamlBytes, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Write(delimiter)
	buf.WriteByte('\n')
	buf.Write(yamlBytes)
	buf.Write(delimiter)
	buf.WriteByte('\n')
	if body != "" {
		buf.WriteString(body)
	}
	return buf.Bytes(), nil
}
