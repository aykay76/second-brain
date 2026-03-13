package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func setupTestServer(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return NewClient(ts.URL)
}

func jsonHandler(t *testing.T, v any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}
}

func TestClientHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", jsonHandler(t, HealthResponse{
		Status:   "ok",
		Database: "up",
	}))
	c := setupTestServer(t, mux)

	resp, err := c.Health()
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("Status = %q, want %q", resp.Status, "ok")
	}
	if resp.Database != "up" {
		t.Errorf("Database = %q, want %q", resp.Database, "up")
	}
}

func TestClientStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", jsonHandler(t, StatusResponse{
		Artifacts: struct {
			Total    int            `json:"total"`
			BySource map[string]int `json:"by_source"`
			ByType   map[string]int `json:"by_type"`
		}{
			Total:    42,
			BySource: map[string]int{"github": 20, "arxiv": 15, "filesystem": 7},
			ByType:   map[string]int{"repo": 15, "paper": 15, "document": 7, "star": 5},
		},
		Embeddings: struct {
			Total    int     `json:"total"`
			Coverage float64 `json:"coverage"`
		}{Total: 40, Coverage: 95.2},
		Relationships: struct {
			Total  int            `json:"total"`
			ByType map[string]int `json:"by_type"`
		}{Total: 10, ByType: map[string]int{"RELATED_TO": 5, "IMPLEMENTS": 3, "BELONGS_TO": 2}},
	}))
	c := setupTestServer(t, mux)

	resp, err := c.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if resp.Artifacts.Total != 42 {
		t.Errorf("Artifacts.Total = %d, want 42", resp.Artifacts.Total)
	}
	if resp.Artifacts.BySource["github"] != 20 {
		t.Errorf("BySource[github] = %d, want 20", resp.Artifacts.BySource["github"])
	}
	if resp.Embeddings.Coverage != 95.2 {
		t.Errorf("Embeddings.Coverage = %f, want 95.2", resp.Embeddings.Coverage)
	}
}

func TestClientSearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, `{"error":"q parameter is required"}`, http.StatusBadRequest)
			return
		}
		limit := r.URL.Query().Get("limit")
		if limit == "" {
			limit = "10"
		}
		url := "https://github.com/example/repo"
		jsonHandler(t, SearchResponse{
			Query: q,
			Count: 1,
			Results: []SearchResult{
				{
					ID:           "abc12345-1234-1234-1234-123456789abc",
					Source:       "github",
					ArtifactType: "repo",
					Title:        "Example Repo",
					SourceURL:    &url,
					Score:        0.85,
					Metadata:     json.RawMessage(`{"language":"Go"}`),
				},
			},
		})(w, r)
		_ = limit
	})
	c := setupTestServer(t, mux)

	resp, err := c.Search("event sourcing", 10, "")
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("Count = %d, want 1", resp.Count)
	}
	if resp.Results[0].Source != "github" {
		t.Errorf("Results[0].Source = %q, want %q", resp.Results[0].Source, "github")
	}
	if resp.Results[0].Score != 0.85 {
		t.Errorf("Results[0].Score = %f, want 0.85", resp.Results[0].Score)
	}
}

func TestClientAsk(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ask", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Question string `json:"question"`
			TopK     int    `json:"top_k"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if body.Question == "" {
			http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
			return
		}

		jsonHandler(t, AskResponse{
			Question: body.Question,
			Answer:   "Event sourcing is a pattern where state changes are stored as a sequence of events [1].",
			Sources: []AskSource{
				{
					Index:        1,
					ArtifactID:   "abc12345",
					Title:        "Event Sourcing Guide",
					Source:       "filesystem",
					ArtifactType: "document",
					Score:        0.92,
					Cited:        true,
				},
			},
		})(w, r)
	})
	c := setupTestServer(t, mux)

	resp, err := c.Ask("what is event sourcing?", 0)
	if err != nil {
		t.Fatalf("Ask() error: %v", err)
	}
	if resp.Answer == "" {
		t.Error("Answer is empty")
	}
	if len(resp.Sources) != 1 {
		t.Fatalf("Sources count = %d, want 1", len(resp.Sources))
	}
	if !resp.Sources[0].Cited {
		t.Error("Sources[0].Cited = false, want true")
	}
}

func TestClientIngest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ingest/github", jsonHandler(t, IngestResponse{
		Source:   "github",
		Ingested: 5,
		Skipped:  10,
		Errors:   0,
	}))
	c := setupTestServer(t, mux)

	resp, err := c.Ingest("github")
	if err != nil {
		t.Fatalf("Ingest() error: %v", err)
	}
	if resp.Ingested != 5 {
		t.Errorf("Ingested = %d, want 5", resp.Ingested)
	}
	if resp.Source != "github" {
		t.Errorf("Source = %q, want %q", resp.Source, "github")
	}
}

func TestClientListArtifacts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /artifacts", func(w http.ResponseWriter, r *http.Request) {
		source := r.URL.Query().Get("source")
		if source != "github_trending" {
			t.Errorf("source = %q, want %q", source, "github_trending")
		}
		jsonHandler(t, ArtifactListResponse{
			Count: 2,
			Artifacts: []Artifact{
				{
					ID:           "aaa11111",
					Source:       "github_trending",
					ArtifactType: "trending_repo",
					Title:        "awesome-go",
					Metadata:     json.RawMessage(`{"language":"Go","stars":5000,"stars_today":120}`),
					CreatedAt:    "2026-03-13",
					IngestedAt:   "2026-03-13",
				},
				{
					ID:           "bbb22222",
					Source:       "github_trending",
					ArtifactType: "trending_repo",
					Title:        "rust-gpu",
					Metadata:     json.RawMessage(`{"language":"Rust","stars":8000,"stars_today":200}`),
					CreatedAt:    "2026-03-13",
					IngestedAt:   "2026-03-13",
				},
			},
		})(w, r)
	})
	c := setupTestServer(t, mux)

	resp, err := c.ListArtifacts("github_trending", "", 10, "recent")
	if err != nil {
		t.Fatalf("ListArtifacts() error: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("Count = %d, want 2", resp.Count)
	}
	if resp.Artifacts[0].Title != "awesome-go" {
		t.Errorf("Artifacts[0].Title = %q, want %q", resp.Artifacts[0].Title, "awesome-go")
	}
}

func TestClientRelated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /artifacts/{id}/related", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		jsonHandler(t, RelatedResponse{
			Artifact: Artifact{
				ID:           id,
				Source:       "arxiv",
				ArtifactType: "paper",
				Title:        "Attention Is All You Need",
				CreatedAt:    "2017-06-12",
				IngestedAt:   "2026-03-13",
				Metadata:     json.RawMessage(`{}`),
			},
			Related: []RelatedArtifact{
				{
					Artifact: Artifact{
						ID:           "rel11111",
						Source:       "github",
						ArtifactType: "repo",
						Title:        "transformer-implementation",
						CreatedAt:    "2024-01-01",
						IngestedAt:   "2026-03-13",
						Metadata:     json.RawMessage(`{}`),
					},
					RelationType: "IMPLEMENTS",
					Confidence:   0.91,
				},
			},
		})(w, r)
	})
	c := setupTestServer(t, mux)

	resp, err := c.Related("abc12345-1234-1234-1234-123456789abc")
	if err != nil {
		t.Fatalf("Related() error: %v", err)
	}
	if resp.Artifact.Title != "Attention Is All You Need" {
		t.Errorf("Artifact.Title = %q, want %q", resp.Artifact.Title, "Attention Is All You Need")
	}
	if len(resp.Related) != 1 {
		t.Fatalf("Related count = %d, want 1", len(resp.Related))
	}
	if resp.Related[0].RelationType != "IMPLEMENTS" {
		t.Errorf("RelationType = %q, want %q", resp.Related[0].RelationType, "IMPLEMENTS")
	}
	if resp.Related[0].Confidence != 0.91 {
		t.Errorf("Confidence = %f, want 0.91", resp.Related[0].Confidence)
	}
}

func TestClientTag(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /artifacts/{id}/tags", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body struct {
			Tag string `json:"tag"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		w.WriteHeader(http.StatusCreated)
		jsonHandler(t, TagResponse{
			ID:         "tag-uuid",
			ArtifactID: id,
			Tag:        body.Tag,
		})(w, r)
	})
	c := setupTestServer(t, mux)

	resp, err := c.Tag("abc12345", "architecture")
	if err != nil {
		t.Fatalf("Tag() error: %v", err)
	}
	if resp.Tag != "architecture" {
		t.Errorf("Tag = %q, want %q", resp.Tag, "architecture")
	}
}

func TestClientDiscover(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /discover", jsonHandler(t, DiscoverResponse{
		CrossSourceRelated: 5,
		TagCoOccurrence:    3,
		AuthorMatches:      2,
		CitationMatches:    1,
		TrendingResearch:   4,
		Total:              15,
	}))
	c := setupTestServer(t, mux)

	resp, err := c.Discover()
	if err != nil {
		t.Fatalf("Discover() error: %v", err)
	}
	if resp.Total != 15 {
		t.Errorf("Total = %d, want 15", resp.Total)
	}
	if resp.CrossSourceRelated != 5 {
		t.Errorf("CrossSourceRelated = %d, want 5", resp.CrossSourceRelated)
	}
}

func TestClientErrorHandling(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "database connection failed"})
	})
	c := setupTestServer(t, mux)

	_, err := c.Health()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "database connection failed" {
		t.Errorf("error = %q, want %q", err.Error(), "database connection failed")
	}
}

func TestFormatTimeAgo(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"invalid-date", "invalid-date"},
		{"2020-01-01T00:00:00Z", "d ago"},
	}

	for _, tt := range tests {
		result := formatTimeAgo(tt.input)
		if result == "" {
			t.Errorf("formatTimeAgo(%q) returned empty string", tt.input)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		max    int
		expect string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is..."},
		{"exact", 5, "exact"},
	}

	for _, tt := range tests {
		result := truncate(tt.input, tt.max)
		if result != tt.expect {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, result, tt.expect)
		}
	}
}

func TestScoreBar(t *testing.T) {
	result := scoreBar(0.85)
	if result == "" {
		t.Error("scoreBar returned empty string")
	}
}

func TestColorSource(t *testing.T) {
	for _, source := range []string{"github", "filesystem", "arxiv", "youtube", "onedrive", "unknown"} {
		result := colorSource(source)
		if result == "" {
			t.Errorf("colorSource(%q) returned empty string", source)
		}
	}
}

func TestIconSource(t *testing.T) {
	for _, source := range []string{"github", "filesystem", "arxiv", "youtube", "onedrive", "unknown"} {
		result := iconSource(source)
		if result == "" {
			t.Errorf("iconSource(%q) returned empty string", source)
		}
	}
}

func TestBarLen(t *testing.T) {
	tests := []struct {
		value, total, maxLen, expect int
	}{
		{0, 100, 30, 0},
		{50, 100, 30, 15},
		{100, 100, 30, 30},
		{1, 100, 30, 1},
		{0, 0, 30, 0},
	}

	for _, tt := range tests {
		result := barLen(tt.value, tt.total, tt.maxLen)
		if result != tt.expect {
			t.Errorf("barLen(%d, %d, %d) = %d, want %d", tt.value, tt.total, tt.maxLen, result, tt.expect)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]int{"a": 1, "b": 3, "c": 2}
	keys := sortedKeys(m)
	if keys[0] != "b" {
		t.Errorf("keys[0] = %q, want %q", keys[0], "b")
	}
	if keys[1] != "c" {
		t.Errorf("keys[1] = %q, want %q", keys[1], "c")
	}
}

func TestRootCmdStructure(t *testing.T) {
	root := NewRootCmd()

	expectedCmds := []string{"ask", "search", "ingest", "trending", "papers", "related", "status", "tag", "discover", "enrich"}
	cmds := make(map[string]bool)
	for _, c := range root.Commands() {
		cmds[c.Name()] = true
	}

	for _, name := range expectedCmds {
		if !cmds[name] {
			t.Errorf("root command missing subcommand %q", name)
		}
	}
}

func TestClientEnrich(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /enrich", jsonHandler(t, EnrichResponse{
		Tagged:     12,
		Summarised: 8,
		Errors:     1,
	}))
	c := setupTestServer(t, mux)

	resp, err := c.Enrich()
	if err != nil {
		t.Fatalf("Enrich() error: %v", err)
	}
	if resp.Tagged != 12 {
		t.Errorf("Tagged = %d, want 12", resp.Tagged)
	}
	if resp.Summarised != 8 {
		t.Errorf("Summarised = %d, want 8", resp.Summarised)
	}
	if resp.Errors != 1 {
		t.Errorf("Errors = %d, want 1", resp.Errors)
	}
}

func TestClientSearchWithTags(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		tags := r.URL.Query().Get("tags")
		if tags != "architecture,database" {
			t.Errorf("tags param = %q, want %q", tags, "architecture,database")
		}
		jsonHandler(t, SearchResponse{
			Query: r.URL.Query().Get("q"),
			Count: 2,
			Results: []SearchResult{
				{ID: "aaa", Title: "Result 1", Source: "filesystem", Score: 0.9},
				{ID: "bbb", Title: "Result 2", Source: "github", Score: 0.8},
			},
		})(w, r)
	})
	c := setupTestServer(t, mux)

	resp, err := c.SearchWithTags("migration patterns", 10, "", []string{"architecture", "database"})
	if err != nil {
		t.Fatalf("SearchWithTags() error: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("Count = %d, want 2", resp.Count)
	}
}
