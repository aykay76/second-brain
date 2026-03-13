package youtube

import (
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

// --- Mock YouTube API responses ---

func mockSearchJSON(videos ...mockVideo) string {
	type id struct {
		VideoID string `json:"videoId"`
	}
	type snip struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		ChannelID    string `json:"channelId"`
		ChannelTitle string `json:"channelTitle"`
		PublishedAt  string `json:"publishedAt"`
	}
	type item struct {
		ID      id   `json:"id"`
		Snippet snip `json:"snippet"`
	}
	type resp struct {
		Items []item `json:"items"`
	}

	r := resp{}
	for _, v := range videos {
		r.Items = append(r.Items, item{
			ID: id{VideoID: v.id},
			Snippet: snip{
				Title:        v.title,
				Description:  v.description,
				ChannelID:    v.channelID,
				ChannelTitle: v.channel,
				PublishedAt:  v.published,
			},
		})
	}
	data, _ := json.Marshal(r)
	return string(data)
}

func mockVideoDetailsJSON(videos ...mockVideo) string {
	type snip struct {
		Tags []string `json:"tags"`
	}
	type cd struct {
		Duration string `json:"duration"`
	}
	type stats struct {
		ViewCount string `json:"viewCount"`
		LikeCount string `json:"likeCount"`
	}
	type item struct {
		ID             string `json:"id"`
		Snippet        snip   `json:"snippet"`
		ContentDetails cd     `json:"contentDetails"`
		Statistics     stats  `json:"statistics"`
	}
	type resp struct {
		Items []item `json:"items"`
	}

	r := resp{}
	for _, v := range videos {
		r.Items = append(r.Items, item{
			ID:             v.id,
			Snippet:        snip{Tags: v.tags},
			ContentDetails: cd{Duration: v.duration},
			Statistics:     stats{ViewCount: v.viewCount, LikeCount: v.likeCount},
		})
	}
	data, _ := json.Marshal(r)
	return string(data)
}

type mockVideo struct {
	id          string
	title       string
	description string
	channelID   string
	channel     string
	published   string
	tags        []string
	duration    string
	viewCount   string
	likeCount   string
}

var sampleVideos = []mockVideo{
	{
		id:          "dQw4w9WgXcQ",
		title:       "Building Microservices in Go",
		description: "A comprehensive talk on building microservices using Go, covering gRPC, service mesh, and observability.",
		channelID:   "UC_test_channel",
		channel:     "GopherCon",
		published:   "2026-03-10T15:00:00Z",
		tags:        []string{"go", "microservices", "grpc"},
		duration:    "PT45M30S",
		viewCount:   "15000",
		likeCount:   "890",
	},
	{
		id:          "abc123def456",
		title:       "RAG Systems Explained",
		description: "Deep dive into Retrieval Augmented Generation systems, embeddings, and vector databases.",
		channelID:   "UC_test_channel",
		channel:     "AI Engineering",
		published:   "2026-03-08T10:00:00Z",
		tags:        []string{"RAG", "LLM", "embeddings", "vector-database"},
		duration:    "PT30M15S",
		viewCount:   "25000",
		likeCount:   "1200",
	},
}

// sampleTranscriptXML provides a minimal timedtext response.
const sampleTranscriptXML = `<?xml version="1.0" encoding="utf-8"?>
<transcript>
  <text start="0.0" dur="3.5">Welcome to this talk on microservices.</text>
  <text start="3.5" dur="4.0">Today we will cover gRPC and service mesh.</text>
  <text start="7.5" dur="3.0">Let us start with the basics.</text>
</transcript>`

// --- Unit tests (no DB) ---

func TestDedup(t *testing.T) {
	t.Parallel()
	videos := []Video{
		{VideoID: "a"},
		{VideoID: "b"},
		{VideoID: "a"},
		{VideoID: "c"},
		{VideoID: "b"},
	}
	got := dedup(videos)
	if len(got) != 3 {
		t.Errorf("dedup: got %d videos, want 3", len(got))
	}
	ids := make([]string, len(got))
	for i, v := range got {
		ids[i] = v.VideoID
	}
	want := "a,b,c"
	if strings.Join(ids, ",") != want {
		t.Errorf("dedup order = %v, want %v", ids, want)
	}
}

func TestBuildEmbeddingText(t *testing.T) {
	t.Parallel()

	t.Run("without transcript", func(t *testing.T) {
		text := buildEmbeddingText("Title", "Description", "")
		if text != "Title\nDescription" {
			t.Errorf("got %q", text)
		}
	})

	t.Run("with short transcript", func(t *testing.T) {
		text := buildEmbeddingText("Title", "Desc", "Some transcript text")
		if !strings.Contains(text, "Some transcript text") {
			t.Errorf("expected transcript in embedding text, got %q", text)
		}
	})

	t.Run("with long transcript truncation", func(t *testing.T) {
		longTranscript := strings.Repeat("word ", 2000)
		text := buildEmbeddingText("Title", "Desc", longTranscript)
		if len(text) > maxEmbeddingContentLen+200 {
			t.Errorf("embedding text too long: %d", len(text))
		}
	})
}

func TestParseTimedText(t *testing.T) {
	t.Parallel()

	text := parseTimedText([]byte(sampleTranscriptXML))
	if text == "" {
		t.Fatal("expected non-empty transcript")
	}
	if !strings.Contains(text, "Welcome to this talk on microservices.") {
		t.Errorf("transcript missing expected text: %q", text)
	}
	if !strings.Contains(text, "gRPC and service mesh") {
		t.Errorf("transcript missing expected text: %q", text)
	}
}

func TestParseTimedTextInvalid(t *testing.T) {
	t.Parallel()
	text := parseTimedText([]byte("not xml"))
	if text != "" {
		t.Errorf("expected empty string for invalid XML, got %q", text)
	}
}

func TestParseCaptionTrackURL(t *testing.T) {
	t.Parallel()

	t.Run("english manual captions", func(t *testing.T) {
		html := `"captionTracks":[{"baseUrl":"https://example.com/timedtext?v=abc\u0026lang=en","languageCode":"en","kind":""},{"baseUrl":"https://example.com/timedtext?v=abc\u0026lang=fr","languageCode":"fr","kind":"asr"}]`
		url := parseCaptionTrackURL(html)
		if !strings.Contains(url, "lang=en") {
			t.Errorf("expected English caption URL, got %q", url)
		}
	})

	t.Run("auto-generated english", func(t *testing.T) {
		html := `"captionTracks":[{"baseUrl":"https://example.com/timedtext?v=abc\u0026lang=en","languageCode":"en","kind":"asr"}]`
		url := parseCaptionTrackURL(html)
		if url == "" {
			t.Error("expected auto-generated caption URL")
		}
	})

	t.Run("no captions", func(t *testing.T) {
		url := parseCaptionTrackURL("<html>no captions here</html>")
		if url != "" {
			t.Errorf("expected empty URL, got %q", url)
		}
	})
}

func TestUnescapeHTML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"hello &amp; world", "hello & world"},
		{"a &lt; b &gt; c", "a < b > c"},
		{"say &quot;hi&quot;", `say "hi"`},
		{"it&#39;s fine", "it's fine"},
		{"no entities", "no entities"},
	}
	for _, tt := range tests {
		got := unescapeHTML(tt.input)
		if got != tt.want {
			t.Errorf("unescapeHTML(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestClamp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n, min, max, want int
	}{
		{5, 1, 10, 5},
		{0, 1, 10, 1},
		{100, 1, 50, 50},
		{-1, 0, 50, 0},
	}
	for _, tt := range tests {
		got := clamp(tt.n, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clamp(%d, %d, %d) = %d, want %d", tt.n, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestStringIntUnmarshal(t *testing.T) {
	t.Parallel()

	t.Run("string value", func(t *testing.T) {
		var s stringInt
		if err := json.Unmarshal([]byte(`"12345"`), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if int64(s) != 12345 {
			t.Errorf("got %d, want 12345", s)
		}
	})

	t.Run("numeric value", func(t *testing.T) {
		var s stringInt
		if err := json.Unmarshal([]byte(`67890`), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if int64(s) != 67890 {
			t.Errorf("got %d, want 67890", s)
		}
	})
}

func TestSyncer_ImplementsSyncer(t *testing.T) {
	t.Parallel()
	s := &Syncer{}
	if s.Name() != "youtube" {
		t.Errorf("Name() = %q, want %q", s.Name(), "youtube")
	}
}

func TestClient_SearchByChannel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		if q.Get("channelId") == "" {
			t.Error("expected channelId parameter")
		}
		if q.Get("type") != "video" {
			t.Errorf("expected type=video, got %q", q.Get("type"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, mockSearchJSON(sampleVideos...))
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.apiBase = server.URL

	videos, err := client.SearchByChannel(context.Background(), "UC_test", time.Time{}, 50)
	if err != nil {
		t.Fatalf("SearchByChannel: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("got %d videos, want 2", len(videos))
	}
	if videos[0].VideoID != "dQw4w9WgXcQ" {
		t.Errorf("VideoID = %q", videos[0].VideoID)
	}
	if videos[0].Title != "Building Microservices in Go" {
		t.Errorf("Title = %q", videos[0].Title)
	}
}

func TestClient_SearchByQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") == "" {
			t.Error("expected q parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, mockSearchJSON(sampleVideos[1]))
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.apiBase = server.URL

	videos, err := client.SearchByQuery(context.Background(), "RAG systems", time.Time{}, 10)
	if err != nil {
		t.Fatalf("SearchByQuery: %v", err)
	}
	if len(videos) != 1 {
		t.Fatalf("got %d videos, want 1", len(videos))
	}
	if videos[0].Title != "RAG Systems Explained" {
		t.Errorf("Title = %q", videos[0].Title)
	}
}

func TestClient_EnrichVideos(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, mockVideoDetailsJSON(sampleVideos...))
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.apiBase = server.URL

	videos := []Video{
		{VideoID: "dQw4w9WgXcQ"},
		{VideoID: "abc123def456"},
	}

	if err := client.EnrichVideos(context.Background(), videos); err != nil {
		t.Fatalf("EnrichVideos: %v", err)
	}

	if videos[0].Duration != "PT45M30S" {
		t.Errorf("Duration = %q, want PT45M30S", videos[0].Duration)
	}
	if videos[0].ViewCount != 15000 {
		t.Errorf("ViewCount = %d, want 15000", videos[0].ViewCount)
	}
	if len(videos[0].Tags) != 3 {
		t.Errorf("Tags = %v, want 3 tags", videos[0].Tags)
	}
}

func TestClient_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error": "forbidden"}`)
	}))
	defer server.Close()

	client := NewClient("bad-key")
	client.apiBase = server.URL

	_, err := client.SearchByChannel(context.Background(), "UC_test", time.Time{}, 10)
	if err == nil {
		t.Error("expected error for HTTP 403")
	}
}

func TestTranscriptFetcher_Fetch(t *testing.T) {
	t.Parallel()

	captionURL := ""
	transcriptServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, sampleTranscriptXML)
	}))
	defer transcriptServer.Close()
	captionURL = transcriptServer.URL + "/timedtext"

	watchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Simulate YouTube page with caption track data.
		fmt.Fprintf(w, `<html><body><script>var ytInitialPlayerResponse = {"captionTracks":[{"baseUrl":"%s","languageCode":"en","kind":"asr"}]};</script></body></html>`, captionURL)
	}))
	defer watchServer.Close()

	fetcher := NewTranscriptFetcher()
	fetcher.watchBase = watchServer.URL

	transcript, err := fetcher.Fetch(context.Background(), "test123")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if transcript == "" {
		t.Fatal("expected non-empty transcript")
	}
	if !strings.Contains(transcript, "Welcome to this talk") {
		t.Errorf("unexpected transcript: %q", transcript)
	}
}

func TestTranscriptFetcher_NoCaptions(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>No captions here</body></html>")
	}))
	defer server.Close()

	fetcher := NewTranscriptFetcher()
	fetcher.watchBase = server.URL

	transcript, err := fetcher.Fetch(context.Background(), "nocaptions")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if transcript != "" {
		t.Errorf("expected empty transcript, got %q", transcript)
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
		db.Exec("DELETE FROM relationships WHERE source_id IN (SELECT id FROM artifacts WHERE source = 'youtube')")
		db.Exec("DELETE FROM artifact_embeddings WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'youtube')")
		db.Exec("DELETE FROM tags WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'youtube')")
		db.Exec("DELETE FROM artifacts WHERE source = 'youtube'")
		db.Exec("DELETE FROM sync_cursors WHERE source_name LIKE 'youtube:%'")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})
	return db
}

func newTestSyncer(t *testing.T, db *sql.DB, apiBase, watchBase string, cfg config.YouTubeConfig) *Syncer {
	t.Helper()
	embedSvc := retrieval.NewEmbeddingService(&stubEmbedder{}, db)
	s := NewSyncer(db, embedSvc, cfg)
	s.client.apiBase = apiBase
	s.transcript.watchBase = watchBase
	return s
}

func newMockYouTubeAPI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search":
			fmt.Fprint(w, mockSearchJSON(sampleVideos...))
		case "/videos":
			fmt.Fprint(w, mockVideoDetailsJSON(sampleVideos...))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newMockWatchPage(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>No captions</body></html>")
	}))
}

func TestSyncer_SyncVideos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockYouTubeAPI(t)
	defer apiServer.Close()
	watchServer := newMockWatchPage(t)
	defer watchServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, watchServer.URL, config.YouTubeConfig{
		Enabled:  true,
		APIKey:   "test-key",
		Channels: []string{"UC_test_channel"},
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
		"SELECT title, content FROM artifacts WHERE source = 'youtube' AND external_id = 'youtube:dQw4w9WgXcQ'",
	).Scan(&title, &content)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if title != "Building Microservices in Go" {
		t.Errorf("title = %q", title)
	}
}

func TestSyncer_SkipsUnchanged(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockYouTubeAPI(t)
	defer apiServer.Close()
	watchServer := newMockWatchPage(t)
	defer watchServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, watchServer.URL, config.YouTubeConfig{
		Enabled:  true,
		APIKey:   "test-key",
		Channels: []string{"UC_test_channel"},
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

	apiServer := newMockYouTubeAPI(t)
	defer apiServer.Close()
	watchServer := newMockWatchPage(t)
	defer watchServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, watchServer.URL, config.YouTubeConfig{
		Enabled:  true,
		APIKey:   "test-key",
		Channels: []string{"UC_test_channel"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var metadataRaw string
	err := db.QueryRowContext(ctx,
		"SELECT metadata FROM artifacts WHERE source = 'youtube' AND external_id = 'youtube:dQw4w9WgXcQ'",
	).Scan(&metadataRaw)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(metadataRaw), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	if ch, _ := meta["channel"].(string); ch != "GopherCon" {
		t.Errorf("channel = %q, want %q", ch, "GopherCon")
	}
	if dur, _ := meta["duration"].(string); dur != "PT45M30S" {
		t.Errorf("duration = %q, want %q", dur, "PT45M30S")
	}
	if views, _ := meta["view_count"].(float64); int(views) != 15000 {
		t.Errorf("view_count = %v, want 15000", meta["view_count"])
	}
}

func TestSyncer_GeneratesEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockYouTubeAPI(t)
	defer apiServer.Close()
	watchServer := newMockWatchPage(t)
	defer watchServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, watchServer.URL, config.YouTubeConfig{
		Enabled:  true,
		APIKey:   "test-key",
		Channels: []string{"UC_test_channel"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_embeddings ae
		JOIN artifacts a ON a.id = ae.artifact_id
		WHERE a.source = 'youtube'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 2 {
		t.Errorf("embeddings = %d, want 2", count)
	}
}

func TestSyncer_SearchTerms(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search":
			q := r.URL.Query().Get("q")
			if q == "" {
				t.Error("expected q parameter for search term")
			}
			fmt.Fprint(w, mockSearchJSON(sampleVideos[0]))
		case "/videos":
			fmt.Fprint(w, mockVideoDetailsJSON(sampleVideos[0]))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiServer.Close()
	watchServer := newMockWatchPage(t)
	defer watchServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, watchServer.URL, config.YouTubeConfig{
		Enabled:     true,
		APIKey:      "test-key",
		SearchTerms: []string{"Go microservices"},
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("ingested = %d, want 1", result.Ingested)
	}
}

func TestSyncer_SetsCursor(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := newMockYouTubeAPI(t)
	defer apiServer.Close()
	watchServer := newMockWatchPage(t)
	defer watchServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, watchServer.URL, config.YouTubeConfig{
		Enabled:  true,
		APIKey:   "test-key",
		Channels: []string{"UC_test_channel"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	cursor := syncer.getCursor(ctx, "youtube:channel:UC_test_channel")
	if cursor.IsZero() {
		t.Error("expected cursor to be set after sync")
	}
}

func TestSyncer_WithTranscript(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search":
			fmt.Fprint(w, mockSearchJSON(sampleVideos[0]))
		case "/videos":
			fmt.Fprint(w, mockVideoDetailsJSON(sampleVideos[0]))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiServer.Close()

	captionURL := ""
	transcriptServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, sampleTranscriptXML)
	}))
	defer transcriptServer.Close()
	captionURL = transcriptServer.URL + "/timedtext"

	watchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><script>"captionTracks":[{"baseUrl":"%s","languageCode":"en","kind":"asr"}]</script></body></html>`, captionURL)
	}))
	defer watchServer.Close()

	syncer := newTestSyncer(t, db, apiServer.URL, watchServer.URL, config.YouTubeConfig{
		Enabled:  true,
		APIKey:   "test-key",
		Channels: []string{"UC_test_channel"},
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var content, metadataRaw string
	err := db.QueryRowContext(ctx,
		"SELECT content, metadata FROM artifacts WHERE source = 'youtube' AND external_id = 'youtube:dQw4w9WgXcQ'",
	).Scan(&content, &metadataRaw)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if !strings.Contains(content, "[Transcript]") {
		t.Error("expected content to contain transcript section")
	}
	if !strings.Contains(content, "Welcome to this talk") {
		t.Error("expected content to contain transcript text")
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(metadataRaw), &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ht, _ := meta["has_transcript"].(bool); !ht {
		t.Error("expected has_transcript to be true")
	}
}
