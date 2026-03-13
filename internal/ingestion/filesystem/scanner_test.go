package filesystem

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

// stubEmbedder satisfies llm.EmbeddingProvider without calling a real LLM.
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
		db.Exec("DELETE FROM relationships WHERE source_id IN (SELECT id FROM artifacts WHERE source = 'filesystem')")
		db.Exec("DELETE FROM artifact_embeddings WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'filesystem')")
		db.Exec("DELETE FROM tags WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'filesystem')")
		db.Exec("DELETE FROM artifacts WHERE source = 'filesystem'")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})

	return db
}

func TestScanner_Sync_IngestsFiles(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, dir, "hello.md", "# Hello\nWorld")
	writeFile(t, dir, "notes.txt", "Some plain text notes")
	writeFile(t, dir, "ignore.go", "package main") // should be filtered out

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{dir},
		Extensions: []string{".md", ".txt"},
	})

	result, err := scanner.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if result.Ingested != 2 {
		t.Errorf("ingested = %d, want 2", result.Ingested)
	}
	if result.Skipped != 0 {
		t.Errorf("skipped = %d, want 0", result.Skipped)
	}
	if result.Errors != 0 {
		t.Errorf("errors = %d, want 0", result.Errors)
	}

	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts WHERE source = 'filesystem'").Scan(&count)
	if count != 2 {
		t.Errorf("artifact count = %d, want 2", count)
	}
}

func TestScanner_Sync_SkipsUnchangedFiles(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, dir, "note.md", "# Test\nOriginal content")

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{dir},
		Extensions: []string{".md"},
	})

	// First sync
	result1, err := scanner.Sync(ctx)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if result1.Ingested != 1 {
		t.Fatalf("first sync ingested = %d, want 1", result1.Ingested)
	}

	// Second sync — same content, should be skipped
	result2, err := scanner.Sync(ctx)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if result2.Skipped != 1 {
		t.Errorf("second sync skipped = %d, want 1", result2.Skipped)
	}
	if result2.Ingested != 0 {
		t.Errorf("second sync ingested = %d, want 0", result2.Ingested)
	}
}

func TestScanner_Sync_UpdatesChangedFiles(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	path := writeFile(t, dir, "note.md", "# Test\nOriginal content")

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{dir},
		Extensions: []string{".md"},
	})

	// First sync
	if _, err := scanner.Sync(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Modify file
	if err := os.WriteFile(path, []byte("# Test\nUpdated content"), 0644); err != nil {
		t.Fatalf("write updated file: %v", err)
	}

	// Second sync — content changed, should be re-ingested
	result, err := scanner.Sync(ctx)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("second sync ingested = %d, want 1", result.Ingested)
	}

	// Verify content was updated
	var content string
	err = db.QueryRowContext(ctx,
		"SELECT content FROM artifacts WHERE source = 'filesystem' AND external_id = $1", path,
	).Scan(&content)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if content != "# Test\nUpdated content" {
		t.Errorf("content = %q, want updated content", content)
	}
}

func TestScanner_Sync_ParsesFrontmatter(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, dir, "with-fm.md", `---
title: My Custom Title
tags:
  - go
  - testing
---
Body content here.`)

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{dir},
		Extensions: []string{".md"},
	})

	if _, err := scanner.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var title string
	var metadataRaw []byte
	err := db.QueryRowContext(ctx,
		"SELECT title, metadata FROM artifacts WHERE source = 'filesystem'",
	).Scan(&title, &metadataRaw)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if title != "My Custom Title" {
		t.Errorf("title = %q, want %q", title, "My Custom Title")
	}

	var meta FileMetadata
	if err := json.Unmarshal(metadataRaw, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.Frontmatter == nil {
		t.Fatal("expected frontmatter in metadata")
	}
	if meta.Frontmatter["title"] != "My Custom Title" {
		t.Errorf("frontmatter title = %v, want %q", meta.Frontmatter["title"], "My Custom Title")
	}
}

func TestScanner_Sync_CreatesWikilinkRelationships(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, dir, "note-a.md", "# Note A\nSee [[note-b]] for details.")
	writeFile(t, dir, "note-b.md", "# Note B\nReferenced from A.")

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{dir},
		Extensions: []string{".md"},
	})

	// First sync ingests both files. Wikilink from A→B may resolve
	// if B is ingested before A's wikilinks are processed.
	// Run sync twice to ensure all links resolve.
	if _, err := scanner.Sync(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Force re-process of note-a to resolve the wikilink to note-b
	noteAPath := filepath.Join(dir, "note-a.md")
	db.ExecContext(ctx, "UPDATE artifacts SET content_hash = 'force-reprocess' WHERE external_id = $1", noteAPath)

	if _, err := scanner.Sync(ctx); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	var relCount int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM relationships WHERE relation_type = 'LINKS_TO'",
	).Scan(&relCount)
	if err != nil {
		t.Fatalf("query relationships: %v", err)
	}
	if relCount != 1 {
		t.Errorf("relationship count = %d, want 1", relCount)
	}
}

func TestScanner_Sync_ExtractsMetadata(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, dir, "test.txt", "Hello, world!")

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{dir},
		Extensions: []string{".txt"},
	})

	if _, err := scanner.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var metadataRaw []byte
	err := db.QueryRowContext(ctx,
		"SELECT metadata FROM artifacts WHERE source = 'filesystem'",
	).Scan(&metadataRaw)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var meta FileMetadata
	if err := json.Unmarshal(metadataRaw, &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if meta.FileName != "test.txt" {
		t.Errorf("file_name = %q, want %q", meta.FileName, "test.txt")
	}
	if meta.Extension != ".txt" {
		t.Errorf("extension = %q, want %q", meta.Extension, ".txt")
	}
	if meta.Size != 13 {
		t.Errorf("size = %d, want 13", meta.Size)
	}
	if meta.Directory != dir {
		t.Errorf("directory = %q, want %q", meta.Directory, dir)
	}
	expectedPath := filepath.Join(dir, "test.txt")
	if meta.FilePath != expectedPath {
		t.Errorf("file_path = %q, want %q", meta.FilePath, expectedPath)
	}
}

func TestScanner_Sync_GeneratesEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, dir, "embed-test.md", "# Embedding Test\nSome content to embed.")

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{dir},
		Extensions: []string{".md"},
	})

	if _, err := scanner.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var embeddingCount int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_embeddings ae
		JOIN artifacts a ON a.id = ae.artifact_id
		WHERE a.source = 'filesystem'`,
	).Scan(&embeddingCount)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if embeddingCount != 1 {
		t.Errorf("embedding count = %d, want 1", embeddingCount)
	}
}

func TestScanner_Sync_RecursesSubdirectories(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub", "nested")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, dir, "root.md", "Root file")
	writeFile(t, subdir, "nested.md", "Nested file")

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{dir},
		Extensions: []string{".md"},
	})

	result, err := scanner.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 2 {
		t.Errorf("ingested = %d, want 2", result.Ingested)
	}
}

func TestScanner_Sync_SkipsNonexistentDirectories(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeFile(t, dir, "real.md", "Real file")

	scanner := NewScanner(db, retrieval.NewEmbeddingService(&stubEmbedder{}, db), config.FilesystemConfig{
		Enabled:    true,
		Paths:      []string{"/nonexistent/path", dir},
		Extensions: []string{".md"},
	})

	result, err := scanner.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("ingested = %d, want 1", result.Ingested)
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// Verify Scanner implements Syncer.
func TestScanner_ImplementsSyncer(t *testing.T) {
	var _ fmt.Stringer // just to use fmt
	s := &Scanner{}
	if s.Name() != "filesystem" {
		t.Errorf("Name() = %q, want %q", s.Name(), "filesystem")
	}
}
