package onedrive

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"pa/internal/config"
	"pa/internal/database"
	"pa/internal/llm"
	"pa/internal/retrieval"
)

// --- Stub embedder ---

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

// --- Text extraction tests ---

func TestExtractText_Markdown(t *testing.T) {
	t.Parallel()
	text, err := ExtractText("notes.md", []byte("# Hello\n\nSome notes here."))
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(text, "Hello") {
		t.Errorf("expected markdown content, got %q", text)
	}
}

func TestExtractText_PlainText(t *testing.T) {
	t.Parallel()
	text, err := ExtractText("readme.txt", []byte("Plain text content"))
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if text != "Plain text content" {
		t.Errorf("got %q, want %q", text, "Plain text content")
	}
}

func TestExtractText_Docx(t *testing.T) {
	t.Parallel()
	docx := createTestDocx(t, "Hello World", "This is a test document.")
	text, err := ExtractText("test.docx", docx)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(text, "Hello World") {
		t.Errorf("expected 'Hello World' in text, got %q", text)
	}
	if !strings.Contains(text, "This is a test document.") {
		t.Errorf("expected 'This is a test document.' in text, got %q", text)
	}
}

func TestExtractText_UnsupportedExtension(t *testing.T) {
	t.Parallel()
	_, err := ExtractText("image.png", []byte("data"))
	if err == nil {
		t.Error("expected error for unsupported extension")
	}
}

func TestIsSupportedExtension(t *testing.T) {
	t.Parallel()
	extensions := []string{".md", ".txt", ".docx", ".pdf"}

	tests := []struct {
		name string
		want bool
	}{
		{"notes.md", true},
		{"readme.txt", true},
		{"report.docx", true},
		{"paper.pdf", true},
		{"image.png", false},
		{"data.csv", false},
		{"UPPER.MD", true},
	}

	for _, tt := range tests {
		got := IsSupportedExtension(tt.name, extensions)
		if got != tt.want {
			t.Errorf("IsSupportedExtension(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestTruncateForEmbedding(t *testing.T) {
	t.Parallel()

	t.Run("short text unchanged", func(t *testing.T) {
		text := "short text"
		got := truncateForEmbedding(text)
		if got != text {
			t.Errorf("got %q, want %q", got, text)
		}
	})

	t.Run("long text truncated", func(t *testing.T) {
		text := strings.Repeat("x", 5000)
		got := truncateForEmbedding(text)
		if len(got) != maxEmbeddingContentLen {
			t.Errorf("len = %d, want %d", len(got), maxEmbeddingContentLen)
		}
	})
}

func TestSha256Hash(t *testing.T) {
	t.Parallel()
	h1 := sha256Hash("hello")
	h2 := sha256Hash("hello")
	h3 := sha256Hash("world")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64", len(h1))
	}
}

// --- Client tests ---

func TestClient_ListFolder(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/children") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{
					"id":   "item-1",
					"name": "notes.md",
					"size": 1234,
					"file": map[string]string{"mimeType": "text/markdown"},
					"lastModifiedDateTime": "2026-03-10T10:00:00Z",
					"parentReference":      map[string]string{"path": "/drive/root:/Documents"},
				},
				{
					"id":     "item-2",
					"name":   "subfolder",
					"folder": map[string]any{},
				},
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	items, err := client.ListFolder(context.Background(), "/Documents")
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Name != "notes.md" {
		t.Errorf("Name = %q, want %q", items[0].Name, "notes.md")
	}
	if items[0].IsFolder {
		t.Error("expected file, got folder")
	}
	if !items[1].IsFolder {
		t.Error("expected folder")
	}
}

func TestClient_DeltaQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{
					"id":   "item-1",
					"name": "updated.md",
					"size": 500,
					"file": map[string]string{"mimeType": "text/markdown"},
					"lastModifiedDateTime": "2026-03-12T12:00:00Z",
				},
				{
					"id":      "item-2",
					"name":    "deleted.txt",
					"deleted": map[string]any{},
				},
			},
			"@odata.deltaLink": "https://graph.microsoft.com/v1.0/delta?token=abc123",
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	resp, err := client.DeltaQuery(context.Background(), "/Documents", "")
	if err != nil {
		t.Fatalf("DeltaQuery: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(resp.Items))
	}
	if resp.Items[0].Name != "updated.md" {
		t.Errorf("Name = %q", resp.Items[0].Name)
	}
	if !resp.Items[1].IsDeleted {
		t.Error("expected item-2 to be deleted")
	}
	if resp.DeltaLink == "" {
		t.Error("expected deltaLink in response")
	}
}

func TestClient_DeltaQueryWithExistingLink(t *testing.T) {
	t.Parallel()

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if !strings.Contains(r.URL.RawQuery, "token=existing") {
			t.Errorf("expected delta link with token=existing, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"value":             []map[string]any{},
			"@odata.deltaLink": "https://graph.microsoft.com/v1.0/delta?token=new",
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	deltaLink := server.URL + "/me/drive/root:/Documents:/delta?token=existing"
	resp, err := client.DeltaQuery(context.Background(), "/Documents", deltaLink)
	if err != nil {
		t.Fatalf("DeltaQuery: %v", err)
	}
	if !called {
		t.Error("expected server to be called")
	}
	if len(resp.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(resp.Items))
	}
}

func TestClient_DownloadContent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "file content here")
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	data, err := client.DownloadContent(context.Background(), server.URL+"/download")
	if err != nil {
		t.Fatalf("DownloadContent: %v", err)
	}
	if string(data) != "file content here" {
		t.Errorf("got %q", string(data))
	}
}

func TestClient_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": "unauthorized"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	_, err := client.ListFolder(context.Background(), "/Documents")
	if err == nil {
		t.Error("expected error for HTTP 401")
	}
}

func TestSyncer_ImplementsSyncer(t *testing.T) {
	t.Parallel()
	s := &Syncer{}
	if s.Name() != "onedrive" {
		t.Errorf("Name() = %q, want %q", s.Name(), "onedrive")
	}
}

func TestDriveItem_Normalize(t *testing.T) {
	t.Parallel()

	t.Run("file item", func(t *testing.T) {
		item := DriveItem{
			ID:   "abc",
			Name: "test.md",
			File: &driveItemFile{MimeType: "text/markdown"},
			ParentReference: &parentRef{Path: "/drive/root:/Documents"},
			LastModifiedJSON: &dateTimeOffset{Time: time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)},
		}
		item.normalize()
		if item.IsFolder {
			t.Error("expected file, not folder")
		}
		if item.MimeType != "text/markdown" {
			t.Errorf("MimeType = %q", item.MimeType)
		}
		if item.ParentPath != "/drive/root:/Documents" {
			t.Errorf("ParentPath = %q", item.ParentPath)
		}
	})

	t.Run("folder item", func(t *testing.T) {
		item := DriveItem{
			ID:     "def",
			Name:   "subfolder",
			Folder: &driveItemFolder{},
		}
		item.normalize()
		if !item.IsFolder {
			t.Error("expected folder")
		}
	})

	t.Run("deleted item", func(t *testing.T) {
		raw := json.RawMessage(`{}`)
		item := DriveItem{
			ID:      "ghi",
			Name:    "old.txt",
			Deleted: &raw,
		}
		item.normalize()
		if !item.IsDeleted {
			t.Error("expected deleted")
		}
	})

	t.Run("download url", func(t *testing.T) {
		url := "https://example.com/download"
		item := DriveItem{
			ID:              "jkl",
			Name:            "file.txt",
			ContentDownload: &url,
		}
		item.normalize()
		if item.DownloadURL != url {
			t.Errorf("DownloadURL = %q", item.DownloadURL)
		}
	})
}

// --- Integration tests (require PA_TEST_DSN) ---

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

	cleanup := func() {
		db.Exec("DELETE FROM relationships WHERE source_id IN (SELECT id FROM artifacts WHERE source = 'onedrive')")
		db.Exec("DELETE FROM artifact_embeddings WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'onedrive')")
		db.Exec("DELETE FROM tags WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'onedrive')")
		db.Exec("DELETE FROM artifacts WHERE source = 'onedrive'")
		db.Exec("DELETE FROM sync_cursors WHERE source_name LIKE 'onedrive:%'")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})
	return db
}

func newTestClient(t *testing.T, graphBase, authBase string) *Client {
	t.Helper()
	client := NewClient(config.OneDriveConfig{
		ClientID: "test-client-id",
		TenantID: "consumers",
	})
	client.graphBase = graphBase
	if authBase != "" {
		client.authBase = authBase
	}
	client.tokens = &tokenStore{
		AccessToken:  "test-token",
		RefreshToken: "test-refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	return client
}

func newTestSyncer(t *testing.T, db *sql.DB, graphBase string, cfg config.OneDriveConfig) *Syncer {
	t.Helper()
	embedSvc := retrieval.NewEmbeddingService(&stubEmbedder{}, db)
	s := NewSyncer(db, embedSvc, cfg)
	s.client.graphBase = graphBase
	s.client.tokens = &tokenStore{
		AccessToken:  "test-token",
		RefreshToken: "test-refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	return s
}

func newMockGraphAPI(t *testing.T) *httptest.Server {
	t.Helper()

	downloadURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(r.URL.Path, "/delta"):
			json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{
						"id":   "item-001",
						"name": "architecture-notes.md",
						"size": 2048,
						"file": map[string]string{"mimeType": "text/markdown"},
						"lastModifiedDateTime":          "2026-03-10T10:00:00Z",
						"parentReference":               map[string]string{"path": "/drive/root:/Documents/Engineering"},
						"@microsoft.graph.downloadUrl": downloadURL + "/download/item-001",
					},
					{
						"id":   "item-002",
						"name": "meeting-notes.txt",
						"size": 512,
						"file": map[string]string{"mimeType": "text/plain"},
						"lastModifiedDateTime":          "2026-03-11T14:30:00Z",
						"parentReference":               map[string]string{"path": "/drive/root:/Documents/Engineering"},
						"@microsoft.graph.downloadUrl": downloadURL + "/download/item-002",
					},
				},
				"@odata.deltaLink": "https://graph.microsoft.com/v1.0/delta?token=test123",
			})

		case strings.Contains(r.URL.Path, "/children"):
			json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{
						"id":   "item-001",
						"name": "architecture-notes.md",
						"size": 2048,
						"file": map[string]string{"mimeType": "text/markdown"},
						"lastModifiedDateTime":          "2026-03-10T10:00:00Z",
						"parentReference":               map[string]string{"path": "/drive/root:/Documents/Engineering"},
						"@microsoft.graph.downloadUrl": downloadURL + "/download/item-001",
					},
				},
			})

		case strings.HasPrefix(r.URL.Path, "/download/"):
			w.Header().Set("Content-Type", "text/plain")
			switch {
			case strings.HasSuffix(r.URL.Path, "item-001"):
				fmt.Fprint(w, "# Architecture Notes\n\nMicroservices patterns and event sourcing approaches.")
			case strings.HasSuffix(r.URL.Path, "item-002"):
				fmt.Fprint(w, "Meeting notes: discussed RAG pipeline improvements and vector search optimization.")
			default:
				w.WriteHeader(http.StatusNotFound)
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	downloadURL = server.URL
	return server
}

func TestSyncer_SyncDocuments(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockGraphAPI(t)
	defer apiServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, config.OneDriveConfig{
		Enabled:    true,
		ClientID:   "test-client",
		Folders:    []string{"/Documents/Engineering"},
		Extensions: []string{".md", ".txt"},
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 2 {
		t.Errorf("ingested = %d, want 2", result.Ingested)
	}

	var title, content string
	err = db.QueryRowContext(ctx,
		"SELECT title, content FROM artifacts WHERE source = 'onedrive' AND external_id = 'onedrive:item-001'",
	).Scan(&title, &content)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if title != "architecture-notes.md" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(content, "Microservices patterns") {
		t.Errorf("content missing expected text: %q", content)
	}
}

func TestSyncer_SkipsUnchanged(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockGraphAPI(t)
	defer apiServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, config.OneDriveConfig{
		Enabled:    true,
		ClientID:   "test-client",
		Folders:    []string{"/Documents/Engineering"},
		Extensions: []string{".md", ".txt"},
	})

	result1, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if result1.Ingested != 2 {
		t.Errorf("first sync ingested = %d, want 2", result1.Ingested)
	}

	result2, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if result2.Skipped != 2 {
		t.Errorf("second sync skipped = %d, want 2", result2.Skipped)
	}
	if result2.Ingested != 0 {
		t.Errorf("second sync ingested = %d, want 0", result2.Ingested)
	}
}

func TestSyncer_StoresMetadata(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockGraphAPI(t)
	defer apiServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, config.OneDriveConfig{
		Enabled:    true,
		ClientID:   "test-client",
		Folders:    []string{"/Documents/Engineering"},
		Extensions: []string{".md", ".txt"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var metadataRaw string
	err := db.QueryRowContext(ctx,
		"SELECT metadata FROM artifacts WHERE source = 'onedrive' AND external_id = 'onedrive:item-001'",
	).Scan(&metadataRaw)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(metadataRaw), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	if name, _ := meta["file_name"].(string); name != "architecture-notes.md" {
		t.Errorf("file_name = %q", name)
	}
	if folder, _ := meta["folder"].(string); folder != "/Documents/Engineering" {
		t.Errorf("folder = %q", folder)
	}
	if size, _ := meta["size"].(float64); int(size) != 2048 {
		t.Errorf("size = %v", meta["size"])
	}
}

func TestSyncer_GeneratesEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockGraphAPI(t)
	defer apiServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, config.OneDriveConfig{
		Enabled:    true,
		ClientID:   "test-client",
		Folders:    []string{"/Documents/Engineering"},
		Extensions: []string{".md", ".txt"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_embeddings ae
		JOIN artifacts a ON a.id = ae.artifact_id
		WHERE a.source = 'onedrive'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 2 {
		t.Errorf("embeddings = %d, want 2", count)
	}
}

func TestSyncer_FiltersExtensions(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockGraphAPI(t)
	defer apiServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, config.OneDriveConfig{
		Enabled:    true,
		ClientID:   "test-client",
		Folders:    []string{"/Documents/Engineering"},
		Extensions: []string{".md"},
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("ingested = %d, want 1 (only .md files)", result.Ingested)
	}
}

func TestSyncer_HandlesDeletion(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockGraphAPI(t)
	defer apiServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, config.OneDriveConfig{
		Enabled:    true,
		ClientID:   "test-client",
		Folders:    []string{"/Documents/Engineering"},
		Extensions: []string{".md", ".txt"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts WHERE source = 'onedrive'").Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 artifacts after initial sync, got %d", count)
	}

	syncer.handleDeletion(ctx, DriveItem{ID: "item-001"})

	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts WHERE source = 'onedrive'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 artifact after deletion, got %d", count)
	}
}

func TestSyncer_SetsCursor(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockGraphAPI(t)
	defer apiServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, config.OneDriveConfig{
		Enabled:    true,
		ClientID:   "test-client",
		Folders:    []string{"/Documents/Engineering"},
		Extensions: []string{".md", ".txt"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	cursor := syncer.getCursor(ctx, "onedrive:delta:/Documents/Engineering")
	if cursor == "" {
		t.Error("expected cursor to be set after sync")
	}
	if !strings.Contains(cursor, "token=test123") {
		t.Errorf("cursor = %q, expected delta link with token", cursor)
	}
}

// --- Helpers ---

func createTestDocx(t *testing.T, paragraphs ...string) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	var xmlContent strings.Builder
	xmlContent.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	xmlContent.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, p := range paragraphs {
		xmlContent.WriteString(`<w:p><w:r><w:t>`)
		xmlContent.WriteString(p)
		xmlContent.WriteString(`</w:t></w:r></w:p>`)
	}
	xmlContent.WriteString(`</w:body></w:document>`)

	f, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte(xmlContent.String()))

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}
