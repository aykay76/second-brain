package devto

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"pa/internal/config"
)

func TestSyncerName(t *testing.T) {
	t.Parallel()

	syncer := &Syncer{}
	assert.Equal(t, "devto", syncer.Name())
}

func TestSyncerDisabled(t *testing.T) {
	t.Parallel()

	syncer := &Syncer{
		cfg: config.DevToConfig{Enabled: false},
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
			name:     "full dev.to URL",
			url:      "https://dev.to/author/article-slug-abc123",
			expected: "article-slug-abc123",
		},
		{
			name:     "dev.to URL with query params",
			url:      "https://dev.to/author/another-article?utm=test",
			expected: "another-article",
		},
		{
			name:     "simple slug",
			url:      "/author/simple-slug",
			expected: "simple-slug",
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

func TestTimeStringEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		isZero bool
	}{
		{
			name:   "empty string",
			input:  "",
			isZero: true,
		},
		{
			name:   "valid RFC3339",
			input:  time.Now().Format(time.RFC3339),
			isZero: false,
		},
		{
			name:   "invalid date string",
			input:  "not a date",
			isZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := timeStringEmpty(tt.input)
			assert.Equal(t, tt.isZero, result)
		})
	}
}
