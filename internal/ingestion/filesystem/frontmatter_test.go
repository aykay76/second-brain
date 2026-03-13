package filesystem

import (
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		content      string
		wantFM       map[string]any
		wantBody     string
		wantHasFM    bool
	}{
		{
			name: "valid frontmatter with title and tags",
			content: `---
title: My Note
tags:
  - go
  - testing
---
This is the body.`,
			wantFM:    map[string]any{"title": "My Note", "tags": []any{"go", "testing"}},
			wantBody:  "This is the body.",
			wantHasFM: true,
		},
		{
			name:      "no frontmatter",
			content:   "Just plain content.\nNo frontmatter here.",
			wantFM:    nil,
			wantBody:  "Just plain content.\nNo frontmatter here.",
			wantHasFM: false,
		},
		{
			name:      "unclosed frontmatter delimiter",
			content:   "---\ntitle: Broken\nNo closing delimiter.",
			wantFM:    nil,
			wantBody:  "---\ntitle: Broken\nNo closing delimiter.",
			wantHasFM: false,
		},
		{
			name:      "empty frontmatter",
			content:   "---\n---\nBody only.",
			wantFM:    nil,
			wantBody:  "Body only.",
			wantHasFM: false,
		},
		{
			name: "frontmatter with leading whitespace",
			content: `  ---
title: Spaced
---
Content here.`,
			wantFM:    map[string]any{"title": "Spaced"},
			wantBody:  "Content here.",
			wantHasFM: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fm, body := ParseFrontmatter(tt.content)

			if tt.wantHasFM && fm == nil {
				t.Fatal("expected frontmatter but got nil")
			}
			if !tt.wantHasFM && fm != nil {
				t.Fatalf("expected no frontmatter but got %v", fm)
			}

			if tt.wantHasFM {
				for k, want := range tt.wantFM {
					got, ok := fm[k]
					if !ok {
						t.Errorf("missing key %q in frontmatter", k)
						continue
					}
					if wantStr, ok := want.(string); ok {
						if gotStr, ok := got.(string); !ok || gotStr != wantStr {
							t.Errorf("key %q: got %v, want %q", k, got, wantStr)
						}
					}
				}
			}

			if body != tt.wantBody {
				t.Errorf("body:\ngot:  %q\nwant: %q", body, tt.wantBody)
			}
		})
	}
}
