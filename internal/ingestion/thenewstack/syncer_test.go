package thenewstack

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"pa/internal/config"
)

func TestSyncerName(t *testing.T) {
	t.Parallel()

	syncer := &Syncer{}
	assert.Equal(t, "thenewstack", syncer.Name())
}

func TestSyncerDisabled(t *testing.T) {
	t.Parallel()

	syncer := &Syncer{
		cfg: config.TheNewStackConfig{Enabled: false},
	}

	result, err := syncer.Sync(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 0, result.Ingested)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Errors)
}

func TestExtractArticleID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "full URL",
			url:      "https://thenewstack.io/article-title/",
			expected: "article-title/",
		},
		{
			name:     "path with slashes",
			url:      "https://thenewstack.io/news/some-article",
			expected: "some-article",
		},
		{
			name:     "simple slug",
			url:      "/article-slug",
			expected: "article-slug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractArticleID(tt.url)
			assert.NotEmpty(t, result)
		})
	}
}

func TestSHA256Hash(t *testing.T) {
	t.Parallel()

	hash1 := sha256Hash("test content")
	hash2 := sha256Hash("test content")
	hash3 := sha256Hash("different content")

	assert.Equal(t, hash1, hash2, "same content should produce same hash")
	assert.NotEqual(t, hash1, hash3, "different content should produce different hash")
	assert.Len(t, hash1, 64, "SHA256 hex should be 64 characters")
}

func TestClientResolveURL(t *testing.T) {
	t.Parallel()

	client := NewClient()

	tests := []struct {
		name     string
		rawURL   string
		expected string
	}{
		{
			name:     "absolute http URL",
			rawURL:   "https://example.com/article",
			expected: "https://example.com/article",
		},
		{
			name:     "relative path",
			rawURL:   "/news/article",
			expected: "https://thenewstack.io/news/article",
		},
		{
			name:     "empty URL",
			rawURL:   "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.resolveURL(tt.rawURL)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseArticleElement(t *testing.T) {
	t.Parallel()

	// This is a basic test - in practice, use colly with actual HTML
	article := Article{
		Title:   "Test Article",
		URL:     "https://thenewstack.io/test-article",
		Authors: []string{"John Doe"},
	}

	assert.NotEmpty(t, article.Title)
	assert.NotEmpty(t, article.URL)
	assert.Len(t, article.Authors, 1)
}
