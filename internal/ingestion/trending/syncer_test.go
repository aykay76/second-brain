package trending

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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

// fixtureHTML mimics GitHub's trending page HTML structure.
const fixtureHTML = `<!DOCTYPE html>
<html><body>
<div class="Box">
  <article class="Box-row">
    <h2 class="h3 lh-condensed">
      <a href="/golang/go" data-view-component="true">
        golang / go
      </a>
    </h2>
    <p class="col-9 color-fg-muted my-1 pr-4">
      The Go programming language
    </p>
    <div class="f6 color-fg-muted mt-2">
      <span class="d-inline-block ml-0 mr-3">
        <span class="repo-language-color" style="background-color: #00ADD8"></span>
        <span itemprop="programmingLanguage">Go</span>
      </span>
      <a class="Link Link--muted d-inline-block mr-3" href="/golang/go/stargazers">
        125,432
      </a>
      <a class="Link Link--muted d-inline-block mr-3" href="/golang/go/forks">
        17,123
      </a>
      <span class="d-inline-block float-sm-right">
        152 stars today
      </span>
    </div>
  </article>
  <article class="Box-row">
    <h2 class="h3 lh-condensed">
      <a href="/facebook/react" data-view-component="true">
        facebook / react
      </a>
    </h2>
    <p class="col-9 color-fg-muted my-1 pr-4">
      The library for web and native user interfaces.
    </p>
    <div class="f6 color-fg-muted mt-2">
      <span class="d-inline-block ml-0 mr-3">
        <span class="repo-language-color" style="background-color: #f1e05a"></span>
        <span itemprop="programmingLanguage">JavaScript</span>
      </span>
      <a class="Link Link--muted d-inline-block mr-3" href="/facebook/react/stargazers">
        230,000
      </a>
      <span class="d-inline-block float-sm-right">
        89 stars today
      </span>
    </div>
  </article>
  <article class="Box-row">
    <h2 class="h3 lh-condensed">
      <a href="/rust-lang/rust" data-view-component="true">
        rust-lang / rust
      </a>
    </h2>
    <p class="col-9 color-fg-muted my-1 pr-4">
      Empowering everyone to build reliable and efficient software.
    </p>
    <div class="f6 color-fg-muted mt-2">
      <span class="d-inline-block ml-0 mr-3">
        <span class="repo-language-color" style="background-color: #dea584"></span>
        <span itemprop="programmingLanguage">Rust</span>
      </span>
      <a class="Link Link--muted d-inline-block mr-3" href="/rust-lang/rust/stargazers">
        99,500
      </a>
      <span class="d-inline-block float-sm-right">
        200 stars today
      </span>
    </div>
  </article>
</div>
</body></html>`

// fixtureEmptyHTML is a valid page with no trending repos.
const fixtureEmptyHTML = `<!DOCTYPE html>
<html><body>
<div class="Box">
  <p>No trending repos found.</p>
</div>
</body></html>`

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

// --- Unit tests (no DB) ---

func TestParseNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{"125,432", 125432},
		{"230,000", 230000},
		{"99500", 99500},
		{"0", 0},
		{"", 0},
		{"1,234,567", 1234567},
		{"  42  ", 42},
	}
	for _, tt := range tests {
		got := parseNumber(tt.input)
		if got != tt.want {
			t.Errorf("parseNumber(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestVelocityRegex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{"152 stars today", 152},
		{"89 stars today", 89},
		{"1,234 stars today", 1234},
		{"5 star today", 5},
		{"200 stars this week", 200},
		{"no match here", 0},
		{"", 0},
	}
	for _, tt := range tests {
		m := velocityRe.FindStringSubmatch(tt.input)
		got := 0
		if len(m) >= 2 {
			got = parseNumber(m[1])
		}
		if got != tt.want {
			t.Errorf("velocity(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestScraper_ScrapePage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer server.Close()

	scraper := &Scraper{baseURL: server.URL}
	repos, err := scraper.scrapePage(context.Background(), server.URL+"/trending?since=daily")
	if err != nil {
		t.Fatalf("scrapePage: %v", err)
	}

	if len(repos) != 3 {
		t.Fatalf("got %d repos, want 3", len(repos))
	}

	// Verify first repo
	r := repos[0]
	if r.FullName != "golang/go" {
		t.Errorf("FullName = %q, want %q", r.FullName, "golang/go")
	}
	if r.Owner != "golang" {
		t.Errorf("Owner = %q, want %q", r.Owner, "golang")
	}
	if r.Name != "go" {
		t.Errorf("Name = %q, want %q", r.Name, "go")
	}
	if r.Description != "The Go programming language" {
		t.Errorf("Description = %q", r.Description)
	}
	if r.Language != "Go" {
		t.Errorf("Language = %q, want %q", r.Language, "Go")
	}
	if r.Stars != 125432 {
		t.Errorf("Stars = %d, want 125432", r.Stars)
	}
	if r.StarsToday != 152 {
		t.Errorf("StarsToday = %d, want 152", r.StarsToday)
	}
	if r.HTMLURL != "https://github.com/golang/go" {
		t.Errorf("HTMLURL = %q", r.HTMLURL)
	}

	// Verify second repo
	if repos[1].FullName != "facebook/react" {
		t.Errorf("second repo FullName = %q", repos[1].FullName)
	}
	if repos[1].Stars != 230000 {
		t.Errorf("second repo Stars = %d, want 230000", repos[1].Stars)
	}
}

func TestScraper_ScrapeDeduplicates(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer server.Close()

	scraper := &Scraper{baseURL: server.URL}
	repos, err := scraper.Scrape(context.Background(), []string{"Go", "Rust"})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}

	// All three pages serve the same HTML, so dedup should give us 3 unique repos.
	if len(repos) != 3 {
		t.Errorf("got %d repos after dedup, want 3", len(repos))
	}
}

func TestScraper_ScrapeEmptyPage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureEmptyHTML)
	}))
	defer server.Close()

	scraper := &Scraper{baseURL: server.URL}
	_, err := scraper.Scrape(context.Background(), nil)
	if err == nil {
		t.Error("expected error for empty scrape")
	}
}

func TestScraper_BuildURLs(t *testing.T) {
	t.Parallel()
	scraper := NewScraper()
	urls := scraper.buildURLs([]string{"Go", "Python", "Rust"})

	if len(urls) != 4 {
		t.Fatalf("got %d URLs, want 4", len(urls))
	}
	if urls[0] != "https://github.com/trending?since=daily" {
		t.Errorf("first URL = %q", urls[0])
	}
	if urls[1] != "https://github.com/trending/go?since=daily" {
		t.Errorf("second URL = %q", urls[1])
	}
	if urls[3] != "https://github.com/trending/rust?since=daily" {
		t.Errorf("fourth URL = %q", urls[3])
	}
}

func TestScraper_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	scraper := &Scraper{baseURL: server.URL}
	_, err := scraper.Scrape(ctx, []string{"Go"})
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestSyncer_ImplementsSyncer(t *testing.T) {
	t.Parallel()
	s := &Syncer{}
	if s.Name() != "trending" {
		t.Errorf("Name() = %q, want %q", s.Name(), "trending")
	}
}

func TestArxivIDRegex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want int
	}{
		{"Implementation of arXiv:2403.12345", 1},
		{"Based on 2301.12345v2 and arXiv:2302.67890", 2},
		{"No references here", 0},
	}
	for _, tt := range tests {
		matches := arxivIDRe.FindAllStringSubmatch(tt.text, -1)
		if len(matches) != tt.want {
			t.Errorf("text %q: got %d matches, want %d", tt.text, len(matches), tt.want)
		}
	}
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
		db.Exec("DELETE FROM relationships WHERE source_id IN (SELECT id FROM artifacts WHERE source = 'github_trending')")
		db.Exec("DELETE FROM artifact_embeddings WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'github_trending')")
		db.Exec("DELETE FROM tags WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'github_trending')")
		db.Exec("DELETE FROM artifacts WHERE source = 'github_trending'")
		db.Exec("DELETE FROM sync_cursors WHERE source_name LIKE 'trending:%'")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})
	return db
}

func newTestSyncer(t *testing.T, db *sql.DB, scraperBaseURL string, cfg config.TrendingConfig) *Syncer {
	t.Helper()
	embedSvc := retrieval.NewEmbeddingService(&stubEmbedder{}, db)
	s := NewSyncer(db, embedSvc, cfg, "")
	s.scraper.baseURL = scraperBaseURL
	return s
}

func TestSyncer_SyncTrendingRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.TrendingConfig{
		Enabled:   true,
		Languages: []string{"Go"},
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 3 {
		t.Errorf("ingested = %d, want 3", result.Ingested)
	}

	var title, content string
	err = db.QueryRowContext(ctx,
		"SELECT title, content FROM artifacts WHERE source = 'github_trending' AND external_id = 'trending:golang/go'",
	).Scan(&title, &content)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if title != "go" {
		t.Errorf("title = %q, want %q", title, "go")
	}
	if content != "The Go programming language" {
		t.Errorf("content = %q", content)
	}
}

func TestSyncer_SkipsUnchanged(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.TrendingConfig{
		Enabled:   true,
		Languages: []string{"Go"},
	})

	result1, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if result1.Ingested != 3 {
		t.Errorf("first sync ingested = %d, want 3", result1.Ingested)
	}

	result2, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if result2.Skipped != 3 {
		t.Errorf("second sync skipped = %d, want 3", result2.Skipped)
	}
	if result2.Ingested != 0 {
		t.Errorf("second sync ingested = %d, want 0", result2.Ingested)
	}
}

func TestSyncer_StoresMetadata(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.TrendingConfig{
		Enabled:   true,
		Languages: []string{"Go"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var metadataRaw string
	err := db.QueryRowContext(ctx,
		"SELECT metadata FROM artifacts WHERE source = 'github_trending' AND external_id = 'trending:golang/go'",
	).Scan(&metadataRaw)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(metadataRaw), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	if lang, _ := meta["language"].(string); lang != "Go" {
		t.Errorf("language = %q, want %q", lang, "Go")
	}
	if stars, _ := meta["stars"].(float64); int(stars) != 125432 {
		t.Errorf("stars = %v, want 125432", meta["stars"])
	}
	if starsToday, _ := meta["stars_today"].(float64); int(starsToday) != 152 {
		t.Errorf("stars_today = %v, want 152", meta["stars_today"])
	}
	if _, ok := meta["trending_date"]; !ok {
		t.Error("missing trending_date in metadata")
	}
}

func TestSyncer_GeneratesEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.TrendingConfig{
		Enabled:   true,
		Languages: []string{"Go"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_embeddings ae
		JOIN artifacts a ON a.id = ae.artifact_id
		WHERE a.source = 'github_trending'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 3 {
		t.Errorf("embeddings = %d, want 3", count)
	}
}

func TestSyncer_SetsCursor(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.TrendingConfig{
		Enabled:   true,
		Languages: []string{"Go"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var cursorVal string
	err := db.QueryRowContext(ctx,
		"SELECT cursor_value FROM sync_cursors WHERE source_name = 'trending:sync'",
	).Scan(&cursorVal)
	if err != nil {
		t.Fatalf("query cursor: %v", err)
	}
	if cursorVal == "" {
		t.Error("expected cursor to be set after sync")
	}
}

func TestSyncer_WithAPIEnrichment(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	trendingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, fixtureHTML)
	}))
	defer trendingServer.Close()

	readmeContent := base64.StdEncoding.EncodeToString([]byte("# Go\n\nThe Go programming language."))
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/golang/go/readme":
			json.NewEncoder(w).Encode(readmeResponse{
				Content:  readmeContent,
				Encoding: "base64",
			})
		case r.URL.Path == "/repos/golang/go":
			json.NewEncoder(w).Encode(repoInfo{
				Description: "The Go programming language",
				Topics:      []string{"golang", "programming-language"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	}))
	defer apiServer.Close()

	embedSvc := retrieval.NewEmbeddingService(&stubEmbedder{}, db)
	syncer := NewSyncer(db, embedSvc, config.TrendingConfig{
		Enabled:     true,
		Languages:   []string{"Go"},
		FetchReadme: true,
	}, "test-token")
	syncer.scraper.baseURL = trendingServer.URL
	syncer.apiBaseURL = apiServer.URL

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 3 {
		t.Errorf("ingested = %d, want 3", result.Ingested)
	}

	var content string
	err = db.QueryRowContext(ctx,
		"SELECT content FROM artifacts WHERE source = 'github_trending' AND external_id = 'trending:golang/go'",
	).Scan(&content)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if content == "The Go programming language" {
		t.Error("expected README to be appended to content, but content is description-only")
	}
}
