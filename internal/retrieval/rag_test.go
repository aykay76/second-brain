package retrieval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"pa/internal/database"
	"pa/internal/llm"
)

// --- Stubs ---

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

type stubChat struct {
	response string
	err      error
	received []llm.Message
}

func (s *stubChat) Complete(_ context.Context, messages []llm.Message) (string, error) {
	s.received = messages
	return s.response, s.err
}

var _ llm.ChatProvider = (*stubChat)(nil)

// --- Unit Tests (no DB) ---

func TestAssembleContext(t *testing.T) {
	t.Parallel()

	url := "https://github.com/example/repo"
	results := []SearchResult{
		{
			ID: "id-1", Source: "github", ArtifactType: "repo",
			Title: "Example Repo", Content: strPtr("Some content about Go"),
			SourceURL: &url, Score: 0.9, Metadata: json.RawMessage("{}"),
		},
		{
			ID: "id-2", Source: "filesystem", ArtifactType: "note",
			Title: "My Notes", Content: strPtr("Notes about databases"),
			Score: 0.7, Metadata: json.RawMessage("{}"),
		},
	}

	sources, contextBlock := assembleContext(results)

	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}

	if sources[0].Index != 1 || sources[1].Index != 2 {
		t.Errorf("source indices: got %d, %d; want 1, 2", sources[0].Index, sources[1].Index)
	}
	if sources[0].ArtifactID != "id-1" {
		t.Errorf("source[0] id: got %q, want %q", sources[0].ArtifactID, "id-1")
	}
	if sources[1].ArtifactID != "id-2" {
		t.Errorf("source[1] id: got %q, want %q", sources[1].ArtifactID, "id-2")
	}

	if !strings.Contains(contextBlock, "[1]") || !strings.Contains(contextBlock, "[2]") {
		t.Error("context block missing numbered source markers")
	}
	if !strings.Contains(contextBlock, "source: github") {
		t.Error("context block missing github source label")
	}
	if !strings.Contains(contextBlock, "url: "+url) {
		t.Error("context block missing source URL")
	}
	if !strings.Contains(contextBlock, "Some content about Go") {
		t.Error("context block missing content")
	}
	if !strings.Contains(contextBlock, "Title: My Notes") {
		t.Error("context block missing second artifact title")
	}
}

func TestAssembleContext_PrefersSummary(t *testing.T) {
	t.Parallel()

	results := []SearchResult{
		{
			ID: "id-1", Source: "filesystem", ArtifactType: "note",
			Title: "Test", Content: strPtr("full long content here"),
			Summary: strPtr("short summary"),
			Metadata: json.RawMessage("{}"),
		},
	}

	_, contextBlock := assembleContext(results)

	if !strings.Contains(contextBlock, "short summary") {
		t.Error("expected summary to be used in context")
	}
	if strings.Contains(contextBlock, "full long content here") {
		t.Error("expected content to NOT be used when summary is present")
	}
}

func TestAssembleContext_TruncatesLongContent(t *testing.T) {
	t.Parallel()

	longContent := strings.Repeat("x", 3000)
	results := []SearchResult{
		{
			ID: "id-1", Source: "filesystem", ArtifactType: "note",
			Title: "Test", Content: &longContent,
			Metadata: json.RawMessage("{}"),
		},
	}

	_, contextBlock := assembleContext(results)

	if !strings.HasSuffix(strings.TrimSpace(contextBlock), "...") {
		t.Error("expected truncated content to end with ...")
	}
	if len(contextBlock) > defaultMaxContentLen+500 {
		t.Errorf("context block too long: %d chars", len(contextBlock))
	}
}

func TestAssembleContext_NoSourceURL(t *testing.T) {
	t.Parallel()

	results := []SearchResult{
		{
			ID: "id-1", Source: "filesystem", ArtifactType: "note",
			Title: "Test", Content: strPtr("some content"),
			Metadata: json.RawMessage("{}"),
		},
	}

	_, contextBlock := assembleContext(results)

	if strings.Contains(contextBlock, "url:") {
		t.Error("should not include url label when source_url is nil")
	}
}

func TestMarkCitedSources(t *testing.T) {
	t.Parallel()

	sources := []Source{
		{Index: 1, ArtifactID: "a"},
		{Index: 2, ArtifactID: "b"},
		{Index: 3, ArtifactID: "c"},
	}

	markCitedSources("Based on [1] and [3], the answer is clear.", sources)

	if !sources[0].Cited {
		t.Error("source[0] should be cited")
	}
	if sources[1].Cited {
		t.Error("source[1] should NOT be cited")
	}
	if !sources[2].Cited {
		t.Error("source[2] should be cited")
	}
}

func TestMarkCitedSources_NoCitations(t *testing.T) {
	t.Parallel()

	sources := []Source{
		{Index: 1, ArtifactID: "a"},
	}

	markCitedSources("I don't have enough info to answer.", sources)

	if sources[0].Cited {
		t.Error("source should NOT be cited when no citation markers present")
	}
}

func TestExtractCitedIndices(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want map[int]bool
	}{
		{"single", "See [1] for details.", map[int]bool{1: true}},
		{"multiple", "From [1] and [3].", map[int]bool{1: true, 3: true}},
		{"adjacent", "[1][2] agree.", map[int]bool{1: true, 2: true}},
		{"none", "No citations here.", map[int]bool{}},
		{"double digit", "See [12] for info.", map[int]bool{12: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractCitedIndices(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d indices, want %d", len(got), len(tc.want))
			}
			for k := range tc.want {
				if !got[k] {
					t.Errorf("missing index %d", k)
				}
			}
		})
	}
}

func TestDeduplicateResults(t *testing.T) {
	t.Parallel()

	primary := []SearchResult{
		{ID: "a", Title: "A"},
		{ID: "b", Title: "B"},
	}
	related := []SearchResult{
		{ID: "b", Title: "B duplicate"},
		{ID: "c", Title: "C"},
	}

	all := deduplicateResults(primary, related)

	if len(all) != 3 {
		t.Fatalf("expected 3 results, got %d", len(all))
	}
	if all[0].ID != "a" || all[1].ID != "b" || all[2].ID != "c" {
		t.Errorf("unexpected order: %s, %s, %s", all[0].ID, all[1].ID, all[2].ID)
	}
	if all[1].Title != "B" {
		t.Error("primary version should be kept over related duplicate")
	}
}

func TestPickContent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		result  SearchResult
		want    string
	}{
		{
			"summary preferred",
			SearchResult{Summary: strPtr("summary"), Content: strPtr("content")},
			"summary",
		},
		{
			"content when no summary",
			SearchResult{Content: strPtr("content")},
			"content",
		},
		{
			"fallback when both nil",
			SearchResult{},
			"(no content available)",
		},
		{
			"content when empty summary",
			SearchResult{Summary: strPtr(""), Content: strPtr("content")},
			"content",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := pickContent(tc.result)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- Integration Tests (require PA_TEST_DSN) ---

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
		db.Exec("DELETE FROM relationships WHERE source_id IN (SELECT id FROM artifacts WHERE source = 'test')")
		db.Exec("DELETE FROM artifact_embeddings WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'test')")
		db.Exec("DELETE FROM tags WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'test')")
		db.Exec("DELETE FROM artifacts WHERE source = 'test'")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})
	return db
}

func insertTestArtifact(t *testing.T, db *sql.DB, embedder *stubEmbedder, title, content string) string {
	t.Helper()
	ctx := context.Background()

	var id string
	err := db.QueryRowContext(ctx, `
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata)
		VALUES ('test', 'note', $1, $2, $3, '{}')
		RETURNING id
	`, "test:"+title, title, content).Scan(&id)
	if err != nil {
		t.Fatalf("insert artifact %q: %v", title, err)
	}

	vecs, err := embedder.Embed(ctx, []string{title + " " + content})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO artifact_embeddings (artifact_id, embedding, model_name)
		VALUES ($1, $2, 'test')
	`, id, pgvector.NewVector(vecs[0]))
	if err != nil {
		t.Fatalf("insert embedding: %v", err)
	}

	return id
}

func TestAsk_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	embedder := &stubEmbedder{}
	chat := &stubChat{
		response: "Based on [1], Go is great for building microservices. It also has excellent concurrency support [2].",
	}

	searchSvc := NewSearchService(embedder, db)
	ragSvc := NewRAGService(searchSvc, chat, db)

	insertTestArtifact(t, db, embedder, "Go Microservices", "Go is excellent for building microservices with small binaries.")
	insertTestArtifact(t, db, embedder, "Go Concurrency", "Goroutines and channels make concurrent programming easy in Go.")

	resp, err := ragSvc.Ask(ctx, AskRequest{Question: "Tell me about Go", TopK: 5})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if resp.Question != "Tell me about Go" {
		t.Errorf("question: got %q, want %q", resp.Question, "Tell me about Go")
	}
	if resp.Answer == "" {
		t.Error("answer should not be empty")
	}
	if len(resp.Sources) == 0 {
		t.Fatal("expected at least one source")
	}

	hasCited := false
	for _, s := range resp.Sources {
		if s.Cited {
			hasCited = true
			break
		}
	}
	if !hasCited {
		t.Error("expected at least one cited source")
	}

	if len(chat.received) != 2 {
		t.Fatalf("expected 2 messages sent to chat, got %d", len(chat.received))
	}
	if chat.received[0].Role != llm.RoleSystem {
		t.Errorf("first message role: got %q, want %q", chat.received[0].Role, llm.RoleSystem)
	}
	if !strings.Contains(chat.received[0].Content, "personal knowledge assistant") {
		t.Error("system prompt should mention personal knowledge assistant")
	}
	if !strings.Contains(chat.received[1].Content, "Tell me about Go") {
		t.Error("user prompt should contain the question")
	}
}

func TestAsk_NoResults(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	embedder := &stubEmbedder{}
	chat := &stubChat{response: "should not be called"}

	searchSvc := NewSearchService(embedder, db)
	ragSvc := NewRAGService(searchSvc, chat, db)

	resp, err := ragSvc.Ask(ctx, AskRequest{Question: "something with no matches"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if !strings.Contains(resp.Answer, "couldn't find") {
		t.Errorf("expected no-results message, got: %q", resp.Answer)
	}
	if len(resp.Sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(resp.Sources))
	}
}

func TestAsk_WithRelatedArtifacts(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	embedder := &stubEmbedder{}
	chat := &stubChat{
		response: "According to [1] and [2], these topics are related.",
	}

	searchSvc := NewSearchService(embedder, db)
	ragSvc := NewRAGService(searchSvc, chat, db)

	id1 := insertTestArtifact(t, db, embedder, "Main Topic", "This is the main topic about testing.")
	id2 := insertTestArtifact(t, db, embedder, "Related Topic", "This is related to the main topic.")

	_, err := db.ExecContext(ctx, `
		INSERT INTO relationships (source_id, target_id, relation_type, confidence)
		VALUES ($1, $2, 'RELATED_TO', 0.9)
	`, id1, id2)
	if err != nil {
		t.Fatalf("insert relationship: %v", err)
	}

	resp, err := ragSvc.Ask(ctx, AskRequest{Question: "Tell me about testing", TopK: 1})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if len(resp.Sources) < 1 {
		t.Fatal("expected at least 1 source")
	}

	userMsg := chat.received[1].Content
	if !strings.Contains(userMsg, "Context:") {
		t.Error("user prompt should contain Context section")
	}
}

func TestAsk_ChatError(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	embedder := &stubEmbedder{}
	chat := &stubChat{err: fmt.Errorf("model unavailable")}

	searchSvc := NewSearchService(embedder, db)
	ragSvc := NewRAGService(searchSvc, chat, db)

	insertTestArtifact(t, db, embedder, "Some Topic", "Some content")

	_, err := ragSvc.Ask(ctx, AskRequest{Question: "test question"})
	if err == nil {
		t.Fatal("expected error when chat fails")
	}
	if !strings.Contains(err.Error(), "chat completion") {
		t.Errorf("error should mention chat completion, got: %v", err)
	}
}

func strPtr(s string) *string {
	return &s
}
