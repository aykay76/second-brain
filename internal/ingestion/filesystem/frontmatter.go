package filesystem

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseFrontmatter extracts YAML frontmatter from markdown content.
// Returns the parsed frontmatter map and the remaining body content.
// If no frontmatter is found, returns nil and the original content.
func ParseFrontmatter(content string) (map[string]any, string) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return nil, content
	}

	// Find closing delimiter — must be on its own line after the opening.
	rest := trimmed[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, content
	}

	fmRaw := strings.TrimSpace(rest[:idx])
	body := strings.TrimSpace(rest[idx+4:])

	var fm map[string]any
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return nil, content
	}

	return fm, body
}
