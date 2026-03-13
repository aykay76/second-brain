package filesystem

import (
	"testing"
)

func TestParseWikilinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "simple wikilinks",
			content: "See [[My Note]] and [[Another Page]].",
			want:    []string{"My Note", "Another Page"},
		},
		{
			name:    "wikilink with alias",
			content: "Check [[page|display text]] for details.",
			want:    []string{"page"},
		},
		{
			name:    "wikilink with heading anchor",
			content: "See [[page#section]] for the section.",
			want:    []string{"page"},
		},
		{
			name:    "wikilink with alias and heading",
			content: "Read [[page#heading|alias text]].",
			want:    []string{"page"},
		},
		{
			name:    "duplicate wikilinks deduplicated",
			content: "[[page]] and again [[page]] and [[page]].",
			want:    []string{"page"},
		},
		{
			name:    "no wikilinks",
			content: "No links here. Just [regular](markdown).",
			want:    nil,
		},
		{
			name:    "mixed wikilinks",
			content: "[[note1]], [[note2|alias]], [[note3#heading]], plain text",
			want:    []string{"note1", "note2", "note3"},
		},
		{
			name:    "empty wikilink ignored",
			content: "Empty [[]] should be skipped.",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ParseWikilinks(tt.content)

			if len(got) != len(tt.want) {
				t.Fatalf("got %d links %v, want %d links %v", len(got), got, len(tt.want), tt.want)
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("link[%d]: got %q, want %q", i, got[i], want)
				}
			}
		})
	}
}
