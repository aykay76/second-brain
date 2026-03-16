package vision

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"pa/internal/config"
	"pa/internal/database"
	"pa/internal/llm"
	"pa/internal/retrieval"
)

// stubEmbedder satisfies llm.EmbeddingProvider for testing.
type stubEmbedder struct{}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, 768)
		for j := range vec {
			vec[j] = float32(i+1) * 0.001
		}
		result[i] = vec
	}
	return result, nil
}

func (s *stubEmbedder) Dimension() int { return 768 }

var _ llm.EmbeddingProvider = (*stubEmbedder)(nil)

// stubVisionProvider satisfies llm.VisionProvider for testing.
type stubVisionProvider struct{}

func (s *stubVisionProvider) Vision(_ context.Context, messages []llm.VisionMessage) (string, error) {
	// Return a simple caption based on message content
	caption := "A beautiful photograph"
	if len(messages) > 0 && messages[len(messages)-1].Content != "" {
		caption = "Caption for: " + messages[len(messages)-1].Content
	}
	return caption, nil
}

var _ llm.VisionProvider = (*stubVisionProvider)(nil)

// setupTestDB connects to the test database specified by PA_TEST_DSN.
// Skips the test if the env var is not set.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("PA_TEST_DSN")
	if dsn == "" {
		t.Skip("PA_TEST_DSN not set, skipping integration test")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping db: %v", err)
	}

	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Clean up test data before and after.
	cleanup := func() {
		db.Exec("DELETE FROM relationships WHERE source_id IN (SELECT id FROM artifacts WHERE source LIKE 'image:%')")
		db.Exec("DELETE FROM artifact_embeddings WHERE artifact_id IN (SELECT id FROM artifacts WHERE source LIKE 'image:%')")
		db.Exec("DELETE FROM tags WHERE artifact_id IN (SELECT id FROM artifacts WHERE source LIKE 'image:%')")
		db.Exec("DELETE FROM artifacts WHERE source LIKE 'image:%'")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})

	return db
}

func TestMetadataExtraction(t *testing.T) {
	// Create a test image file (minimal PNG)
	tmpDir := t.TempDir()
	testImagePath := filepath.Join(tmpDir, "test.png")

	// Create a minimal valid PNG file
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
	}
	if err := os.WriteFile(testImagePath, pngData, 0644); err != nil {
		t.Fatalf("write test image: %v", err)
	}

	vs := NewVisionService(&stubVisionProvider{})
	metadata, err := vs.ExtractMetadata(testImagePath)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}

	if metadata == nil {
		t.Fatal("metadata is nil")
	}

	if metadata.FileName != "test.png" {
		t.Errorf("filename: got %q, want %q", metadata.FileName, "test.png")
	}

	if metadata.FileSize == 0 {
		t.Errorf("file size should be > 0, got %d", metadata.FileSize)
	}

	if metadata.FilePath != testImagePath {
		t.Errorf("path: got %q, want %q", metadata.FilePath, testImagePath)
	}
}

func TestFilesystemSyncer_ProcessImage(t *testing.T) {
	db := setupTestDB(t)
	embedSvc := retrieval.NewEmbeddingService(&stubEmbedder{}, db)
	visionSvc := NewVisionService(&stubVisionProvider{})

	cfg := config.VisionConfig{
		Enabled:          true,
		Extensions:       []string{".png", ".jpg"},
		CaptioningPrompt: "Describe this image",
	}

	syncer := NewFilesystemSyncer(db, embedSvc, visionSvc, cfg)
	syncer.skipCaption = true // Don't call vision model in test

	// Create a test image
	tmpDir := t.TempDir()
	testImagePath := filepath.Join(tmpDir, "test.png")
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	}
	if err := os.WriteFile(testImagePath, pngData, 0644); err != nil {
		t.Fatalf("write test image: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ingested, err := syncer.processImage(ctx, testImagePath)
	if err != nil {
		t.Fatalf("process image: %v", err)
	}

	if !ingested {
		t.Fatal("image should have been ingested")
	}

	// Verify the image was stored in the database
	var count int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifacts
		WHERE source LIKE 'image:%' AND artifact_type = $1
	`, artifactType).Scan(&count)

	if err != nil {
		t.Fatalf("query artifacts: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 artifact, got %d", count)
	}

	// Test idempotency: processing the same image again shouldn't create duplicates
	ingested2, err := syncer.processImage(ctx, testImagePath)
	if err != nil {
		t.Fatalf("process image second time: %v", err)
	}

	if ingested2 {
		t.Fatal("second processing should return ingested=false (already exists)")
	}

	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifacts
		WHERE source LIKE 'image:%' AND artifact_type = $1
	`, artifactType).Scan(&count)

	if err != nil {
		t.Fatalf("query artifacts: %v", err)
	}

	if count != 1 {
		t.Errorf("expected still 1 artifact after reprocessing, got %d", count)
	}
}

func TestFilesystemSyncer_Sync(t *testing.T) {
	db := setupTestDB(t)
	embedSvc := retrieval.NewEmbeddingService(&stubEmbedder{}, db)
	visionSvc := NewVisionService(&stubVisionProvider{})

	// Create test images
	tmpDir := t.TempDir()
	for i := 0; i < 3; i++ {
		path := filepath.Join(tmpDir, "test"+string(rune(i+48))+".png")
		pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if err := os.WriteFile(path, pngData, 0644); err != nil {
			t.Fatalf("write test image: %v", err)
		}
	}

	cfg := config.VisionConfig{
		Enabled:    true,
		Paths:      []string{tmpDir},
		Extensions: []string{".png"},
	}

	syncer := NewFilesystemSyncer(db, embedSvc, visionSvc, cfg)
	syncer.skipCaption = true

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if result.Ingested != 3 {
		t.Errorf("expected 3 ingested, got %d", result.Ingested)
	}

	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}
}
