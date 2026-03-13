package arxiv

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"pa/internal/config"
	"pa/internal/database"
	"pa/internal/llm"
	"pa/internal/retrieval"
)

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

// --- Test helpers ---

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
		db.Exec("DELETE FROM relationships WHERE source_id IN (SELECT id FROM artifacts WHERE source = 'arxiv')")
		db.Exec("DELETE FROM artifact_embeddings WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'arxiv')")
		db.Exec("DELETE FROM tags WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'arxiv')")
		db.Exec("DELETE FROM artifacts WHERE source = 'arxiv'")
		db.Exec("DELETE FROM sync_cursors WHERE source_name LIKE 'arxiv:%'")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})
	return db
}

func newTestSyncer(t *testing.T, db *sql.DB, serverURL string, cfg config.ArXivConfig) *Syncer {
	t.Helper()
	embedSvc := retrieval.NewEmbeddingService(&stubEmbedder{}, db)
	s := NewSyncer(db, embedSvc, cfg)
	s.client.baseURL = serverURL
	return s
}

func sampleAtomFeed(entries ...atomEntry) []byte {
	feed := atomFeed{
		TotalResults: len(entries),
		StartIndex:   0,
		ItemsPerPage: len(entries),
		Entries:      entries,
	}
	data, _ := xml.Marshal(feed)
	return data
}

func sampleEntry(id, title, abstract, published string, authors []string, categories []string) atomEntry {
	e := atomEntry{
		ID:        "http://arxiv.org/abs/" + id,
		Title:     title,
		Summary:   abstract,
		Published: published,
		Updated:   published,
		Links: []atomLink{
			{Href: "http://arxiv.org/abs/" + id, Rel: "alternate", Type: "text/html"},
			{Href: "http://arxiv.org/pdf/" + id, Rel: "related", Type: "application/pdf", Title: "pdf"},
		},
	}
	for _, a := range authors {
		e.Authors = append(e.Authors, atomAuthor{Name: a})
	}
	for _, c := range categories {
		e.Categories = append(e.Categories, atomCat{Term: c})
	}
	return e
}

// --- Unit tests (no DB) ---

func TestExtractArXivID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"http://arxiv.org/abs/2403.12345v1", "2403.12345v1"},
		{"http://arxiv.org/abs/2403.12345", "2403.12345"},
		{"https://arxiv.org/abs/cs/0112017v1", "cs/0112017v1"},
		{"2403.12345v1", "2403.12345v1"},
	}
	for _, tt := range tests {
		got := extractArXivID(tt.input)
		if got != tt.want {
			t.Errorf("extractArXivID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormaliseArXivID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"2403.12345v1", "2403.12345"},
		{"2403.12345v2", "2403.12345"},
		{"2403.12345", "2403.12345"},
		{"cs/0112017v1", "cs/0112017"},
	}
	for _, tt := range tests {
		got := normaliseArXivID(tt.input)
		if got != tt.want {
			t.Errorf("normaliseArXivID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormaliseWhitespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"  hello   world  ", "hello world"},
		{"no\nextra\n  spaces", "no extra spaces"},
		{"already clean", "already clean"},
	}
	for _, tt := range tests {
		got := normaliseWhitespace(tt.input)
		if got != tt.want {
			t.Errorf("normaliseWhitespace(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildSearchQuery(t *testing.T) {
	t.Parallel()

	t.Run("categories only", func(t *testing.T) {
		q := buildSearchQuery([]string{"cs.AI", "cs.SE"}, nil, time.Time{}, time.Time{})
		if q != "(cat:cs.AI+OR+cat:cs.SE)" {
			t.Errorf("unexpected query: %s", q)
		}
	})

	t.Run("keywords only", func(t *testing.T) {
		q := buildSearchQuery(nil, []string{"RAG"}, time.Time{}, time.Time{})
		if q == "" {
			t.Error("expected non-empty query")
		}
	})

	t.Run("with date range", func(t *testing.T) {
		from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC)
		q := buildSearchQuery([]string{"cs.AI"}, nil, from, to)
		if q == "" {
			t.Error("expected non-empty query")
		}
		// Should contain the date range
		if !contains(q, "submittedDate:") {
			t.Errorf("expected date range in query: %s", q)
		}
	})

	t.Run("empty", func(t *testing.T) {
		q := buildSearchQuery(nil, nil, time.Time{}, time.Time{})
		if q != "" {
			t.Errorf("expected empty query, got %q", q)
		}
	})
}

func TestParseFeed(t *testing.T) {
	t.Parallel()
	entries := []atomEntry{
		sampleEntry("2403.12345v1", "Test Paper", "This is the abstract.", "2024-03-15T00:00:00Z",
			[]string{"Alice", "Bob"}, []string{"cs.AI", "cs.SE"}),
		sampleEntry("2403.67890v1", "Another Paper", "Another abstract.", "2024-03-16T00:00:00Z",
			[]string{"Charlie"}, []string{"cs.DC"}),
	}
	feed := &atomFeed{
		TotalResults: 2,
		Entries:      entries,
	}

	papers := parseFeed(feed)
	if len(papers) != 2 {
		t.Fatalf("got %d papers, want 2", len(papers))
	}

	p := papers[0]
	if p.ArXivID != "2403.12345v1" {
		t.Errorf("ArXivID = %q", p.ArXivID)
	}
	if p.Title != "Test Paper" {
		t.Errorf("Title = %q", p.Title)
	}
	if len(p.Authors) != 2 {
		t.Errorf("Authors = %v, want 2", p.Authors)
	}
	if len(p.Categories) != 2 {
		t.Errorf("Categories = %v, want 2", p.Categories)
	}
	if p.PDFURL != "http://arxiv.org/pdf/2403.12345v1" {
		t.Errorf("PDFURL = %q", p.PDFURL)
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		fallback time.Duration
		want     time.Duration
	}{
		{"30d", time.Hour, 30 * 24 * time.Hour},
		{"7d", time.Hour, 7 * 24 * time.Hour},
		{"2w", time.Hour, 14 * 24 * time.Hour},
		{"", time.Hour, time.Hour},
		{"invalid", time.Hour, time.Hour},
		{"24h", time.Hour, 24 * time.Hour},
	}
	for _, tt := range tests {
		got := parseDuration(tt.input, tt.fallback)
		if got != tt.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestArXivIDRegex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want int
	}{
		{"See arXiv:2301.12345 for details", 1},
		{"Based on 2301.12345v2 and arXiv:2302.67890", 2},
		{"No references here", 0},
		{"Paper 2301.12345 and 2302.67890v1", 2},
	}
	for _, tt := range tests {
		matches := arxivIDRe.FindAllStringSubmatch(tt.text, -1)
		if len(matches) != tt.want {
			t.Errorf("text %q: got %d matches, want %d", tt.text, len(matches), tt.want)
		}
	}
}

func TestSyncer_ImplementsSyncer(t *testing.T) {
	t.Parallel()
	s := &Syncer{}
	if s.Name() != "arxiv" {
		t.Errorf("Name() = %q, want %q", s.Name(), "arxiv")
	}
}

// --- Integration tests (require PA_TEST_DSN) ---

func TestSyncer_SyncPapers(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(sampleAtomFeed(
			sampleEntry("2403.12345v1", "Deep Learning for Code", "Abstract about deep learning applied to code.",
				"2024-03-15T00:00:00Z", []string{"Alice", "Bob"}, []string{"cs.AI", "cs.SE"}),
			sampleEntry("2403.67890v1", "RAG Systems Survey", "A survey of retrieval augmented generation.",
				"2024-03-16T00:00:00Z", []string{"Charlie"}, []string{"cs.AI"}),
		))
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.ArXivConfig{
		Enabled:         true,
		Categories:      []string{"cs.AI"},
		InitialLookback: "7d",
		MaxResults:      100,
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
		"SELECT title, content FROM artifacts WHERE source = 'arxiv' AND external_id = 'arxiv:2403.12345v1'",
	).Scan(&title, &content)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if title != "Deep Learning for Code" {
		t.Errorf("title = %q, want %q", title, "Deep Learning for Code")
	}
	if content != "Abstract about deep learning applied to code." {
		t.Errorf("content = %q", content)
	}
}

func TestSyncer_SkipsUnchanged(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	entry := sampleEntry("2403.12345v1", "Test Paper", "Test abstract.",
		"2024-03-15T00:00:00Z", []string{"Alice"}, []string{"cs.AI"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(sampleAtomFeed(entry))
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.ArXivConfig{
		Enabled:         true,
		Categories:      []string{"cs.AI"},
		InitialLookback: "7d",
	})

	result1, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if result1.Ingested != 1 {
		t.Errorf("first sync ingested = %d, want 1", result1.Ingested)
	}

	result2, err := syncer.Sync(ctx)
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

func TestSyncer_IncrementalCursor(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(sampleAtomFeed(
			sampleEntry("2403.11111v1", "Paper One", "Abstract one.",
				"2024-03-15T00:00:00Z", []string{"Alice"}, []string{"cs.AI"}),
		))
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.ArXivConfig{
		Enabled:         true,
		Categories:      []string{"cs.AI"},
		InitialLookback: "7d",
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	cursor := syncer.getCursor(ctx, "arxiv:sync")
	if cursor.IsZero() {
		t.Error("expected cursor to be set after sync")
	}
}

func TestSyncer_ExtractsCitations(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(sampleAtomFeed(
			sampleEntry("2403.11111v1", "Referenced Paper", "A foundational paper.",
				"2024-03-10T00:00:00Z", []string{"Alice"}, []string{"cs.AI"}),
			sampleEntry("2403.22222v1", "Citing Paper", "Extends the work from arXiv:2403.11111 significantly.",
				"2024-03-15T00:00:00Z", []string{"Bob"}, []string{"cs.AI"}),
		))
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.ArXivConfig{
		Enabled:         true,
		Categories:      []string{"cs.AI"},
		InitialLookback: "30d",
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var relCount int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM relationships WHERE relation_type = 'CITES'",
	).Scan(&relCount)
	if err != nil {
		t.Fatalf("query relationships: %v", err)
	}
	if relCount == 0 {
		t.Error("expected at least one CITES relationship")
	}
}

func TestSyncer_GeneratesEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(sampleAtomFeed(
			sampleEntry("2403.99999v1", "Embedding Test", "Test paper for embeddings.",
				"2024-03-15T00:00:00Z", []string{"Alice"}, []string{"cs.AI"}),
		))
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.ArXivConfig{
		Enabled:         true,
		Categories:      []string{"cs.AI"},
		InitialLookback: "7d",
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var embeddingCount int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_embeddings ae
		JOIN artifacts a ON a.id = ae.artifact_id
		WHERE a.source = 'arxiv'`,
	).Scan(&embeddingCount)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if embeddingCount == 0 {
		t.Error("expected at least one embedding for arxiv artifacts")
	}
}

func TestSyncer_StoresMetadata(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(sampleAtomFeed(
			sampleEntry("2403.55555v1", "Metadata Paper", "Abstract.",
				"2024-03-15T00:00:00Z", []string{"Alice", "Bob"}, []string{"cs.AI", "cs.SE"}),
		))
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.ArXivConfig{
		Enabled:         true,
		Categories:      []string{"cs.AI"},
		InitialLookback: "7d",
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var metadataRaw string
	err := db.QueryRowContext(ctx,
		"SELECT metadata FROM artifacts WHERE source = 'arxiv' AND external_id = 'arxiv:2403.55555v1'",
	).Scan(&metadataRaw)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if !contains(metadataRaw, "2403.55555v1") {
		t.Errorf("metadata should contain arxiv_id, got: %s", metadataRaw)
	}
	if !contains(metadataRaw, "Alice") || !contains(metadataRaw, "Bob") {
		t.Errorf("metadata should contain authors, got: %s", metadataRaw)
	}
	if !contains(metadataRaw, "cs.AI") || !contains(metadataRaw, "cs.SE") {
		t.Errorf("metadata should contain categories, got: %s", metadataRaw)
	}
}

func TestClient_SearchWithMock(t *testing.T) {
	t.Parallel()
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		q := r.URL.Query().Get("search_query")
		if q == "" {
			t.Error("expected search_query parameter")
		}
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(sampleAtomFeed(
			sampleEntry("2403.12345v1", "Test", "Abstract.", "2024-03-15T00:00:00Z",
				[]string{"Alice"}, []string{"cs.AI"}),
		))
	}))
	defer server.Close()

	client := NewClient()
	client.baseURL = server.URL

	from := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)

	papers, err := client.Search(context.Background(), []string{"cs.AI"}, nil, from, to, 100)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(papers) != 1 {
		t.Errorf("got %d papers, want 1", len(papers))
	}
	if requestCount == 0 {
		t.Error("expected at least one request")
	}
}

func TestClient_SearchEmptyQuery(t *testing.T) {
	t.Parallel()
	client := NewClient()
	_, err := client.Search(context.Background(), nil, nil, time.Time{}, time.Time{}, 100)
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestClient_SearchHTTPError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error")
	}))
	defer server.Close()

	client := NewClient()
	client.baseURL = server.URL

	_, err := client.Search(context.Background(), []string{"cs.AI"}, nil, time.Time{}, time.Time{}, 100)
	if err == nil {
		t.Error("expected error on HTTP 500")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
