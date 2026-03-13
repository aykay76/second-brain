package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"pa/internal/config"
	"pa/internal/database"
)

// --- Unit tests (no DB) ---

func TestNormaliseAuthorName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"John Smith", "john smith"},
		{" Alice   Bob ", "alice bob"},
		{"J. Doe", "j doe"},
		{"some-hyphenated-name", "some hyphenated name"},
		{"under_score", "under score"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normaliseAuthorName(tt.input)
		if got != tt.want {
			t.Errorf("normaliseAuthorName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAuthorNameMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want bool
	}{
		{"john smith", "john smith", true},
		{"john smith", "jsmith", true},
		{"jsmith", "john smith", true},
		{"john smith", "jane doe", false},
		{"alice", "bob", false},
		{"", "anything", false},
		{"anything", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got := authorNameMatch(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("authorNameMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestPqStringArrayScanner(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input any
		want  []string
	}{
		{"{foo,bar,baz}", []string{"foo", "bar", "baz"}},
		{`{"hello world","test"}`, []string{"hello world", "test"}},
		{"{}", nil},
		{nil, nil},
		{[]byte("{a,b}"), []string{"a", "b"}},
	}
	for _, tt := range tests {
		var dest []string
		scanner := pqStringArray(&dest)
		if err := scanner.Scan(tt.input); err != nil {
			t.Errorf("scan(%v): %v", tt.input, err)
			continue
		}
		if len(dest) != len(tt.want) {
			t.Errorf("scan(%v): got %v, want %v", tt.input, dest, tt.want)
			continue
		}
		for i := range dest {
			if dest[i] != tt.want[i] {
				t.Errorf("scan(%v)[%d]: got %q, want %q", tt.input, i, dest[i], tt.want[i])
			}
		}
	}
}

func TestGitHubURLRegex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want int
	}{
		{"Code at https://github.com/user/repo", 1},
		{"See http://github.com/org/project.git for details", 1},
		{"https://github.com/a/b and https://github.com/c/d", 2},
		{"No URLs here", 0},
		{"https://gitlab.com/user/repo", 0},
	}
	for _, tt := range tests {
		matches := githubURLRe.FindAllStringSubmatch(tt.text, -1)
		if len(matches) != tt.want {
			t.Errorf("text %q: got %d matches, want %d", tt.text, len(matches), tt.want)
		}
	}
}

func TestNewEngine_Defaults(t *testing.T) {
	t.Parallel()
	e := NewEngine(nil, config.DiscoveryConfig{})
	if e.cfg.MaxCandidates != 10 {
		t.Errorf("MaxCandidates = %d, want 10", e.cfg.MaxCandidates)
	}
	if e.cfg.BatchSize != 50 {
		t.Errorf("BatchSize = %d, want 50", e.cfg.BatchSize)
	}
	if e.cfg.SimilarityThreshold != thresholdRelatedTo {
		t.Errorf("SimilarityThreshold = %f, want %f", e.cfg.SimilarityThreshold, thresholdRelatedTo)
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

	cleanupDB(db)
	t.Cleanup(func() {
		cleanupDB(db)
		db.Close()
	})
	return db
}

func cleanupDB(db *sql.DB) {
	db.Exec("DELETE FROM relationships")
	db.Exec("DELETE FROM tags")
	db.Exec("DELETE FROM artifact_embeddings")
	db.Exec("DELETE FROM sync_cursors WHERE source_name LIKE 'discovery:%'")
	db.Exec("DELETE FROM artifacts")
}

func insertArtifact(t *testing.T, db *sql.DB, source, artifactType, externalID, title, content string, metadata map[string]any) string {
	t.Helper()
	metaJSON, _ := json.Marshal(metadata)
	var id string
	err := db.QueryRow(`
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`, source, artifactType, externalID, title, content, metaJSON,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert artifact %q: %v", title, err)
	}
	return id
}

func insertEmbedding(t *testing.T, db *sql.DB, artifactID string, vec []float32) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO artifact_embeddings (artifact_id, embedding, model_name)
		VALUES ($1, $2, 'test')`,
		artifactID, pgvector.NewVector(vec),
	)
	if err != nil {
		t.Fatalf("insert embedding for %s: %v", artifactID, err)
	}
}

func insertTag(t *testing.T, db *sql.DB, artifactID, tag string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO tags (artifact_id, tag, auto_generated) VALUES ($1, $2, true)
		ON CONFLICT DO NOTHING`, artifactID, tag,
	)
	if err != nil {
		t.Fatalf("insert tag %q for %s: %v", tag, artifactID, err)
	}
}

// makeVec creates a 768-dim vector with the given base value.
func makeVec(base float32) []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = base
	}
	return v
}

// makeSimilarVec creates a 768-dim vector close to the given base value
// but with a small offset to create slightly different but similar vectors.
func makeSimilarVec(base, offset float32) []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = base + offset*float32(i%3)*0.001
	}
	return v
}

func newTestEngine(db *sql.DB) *Engine {
	return NewEngine(db, config.DiscoveryConfig{
		Enabled:             true,
		SimilarityThreshold: 0.80,
		MaxCandidates:       10,
		BatchSize:           50,
	})
}

func countRelationships(t *testing.T, db *sql.DB, relationType string) int {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT count(*) FROM relationships WHERE relation_type = $1`, relationType).Scan(&count)
	if err != nil {
		t.Fatalf("count relationships: %v", err)
	}
	return count
}

func TestDiscoverCrossSourceSimilarity(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create two artifacts from different sources with nearly identical embeddings.
	noteID := insertArtifact(t, db, "filesystem", "note", "note1", "Go Concurrency Patterns", "Goroutines and channels", nil)
	repoID := insertArtifact(t, db, "github", "repo", "user/go-patterns", "Go Patterns", "Concurrency patterns in Go", nil)

	vec := makeVec(0.5)
	insertEmbedding(t, db, noteID, vec)
	insertEmbedding(t, db, repoID, makeSimilarVec(0.5, 0.01))

	engine := newTestEngine(db)
	count, err := engine.discoverCrossSourceSimilarity(ctx)
	if err != nil {
		t.Fatalf("discoverCrossSourceSimilarity: %v", err)
	}
	if count == 0 {
		t.Error("expected at least 1 cross-source relationship, got 0")
	}

	got := countRelationships(t, db, "RELATED_TO")
	if got == 0 {
		t.Error("expected RELATED_TO relationships in DB")
	}
}

func TestDiscoverCrossSourceSimilarity_SameSource_Skipped(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Two artifacts from the SAME source should NOT create relationships.
	id1 := insertArtifact(t, db, "github", "repo", "user/repo1", "Repo 1", "Go project", nil)
	id2 := insertArtifact(t, db, "github", "repo", "user/repo2", "Repo 2", "Go project", nil)

	vec := makeVec(0.5)
	insertEmbedding(t, db, id1, vec)
	insertEmbedding(t, db, id2, vec)

	engine := newTestEngine(db)
	count, err := engine.discoverCrossSourceSimilarity(ctx)
	if err != nil {
		t.Fatalf("discoverCrossSourceSimilarity: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 relationships for same-source artifacts, got %d", count)
	}
}

func TestDiscoverTagCoOccurrence(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	noteID := insertArtifact(t, db, "filesystem", "note", "note-arch", "Architecture Notes", "Microservices patterns", nil)
	paperID := insertArtifact(t, db, "arxiv", "paper", "arxiv:2403.11111", "Microservice Architectures", "A survey of patterns", nil)

	insertTag(t, db, noteID, "architecture")
	insertTag(t, db, noteID, "microservices")
	insertTag(t, db, paperID, "architecture")
	insertTag(t, db, paperID, "microservices")

	engine := newTestEngine(db)
	count, err := engine.discoverTagCoOccurrence(ctx)
	if err != nil {
		t.Fatalf("discoverTagCoOccurrence: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 tag co-occurrence relationship, got %d", count)
	}

	got := countRelationships(t, db, "SIMILAR_TOPIC")
	if got != 1 {
		t.Errorf("expected 1 SIMILAR_TOPIC relationship, got %d", got)
	}
}

func TestDiscoverTagCoOccurrence_SingleTag_Skipped(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	noteID := insertArtifact(t, db, "filesystem", "note", "note1", "Note 1", "content", nil)
	paperID := insertArtifact(t, db, "arxiv", "paper", "arxiv:0001", "Paper 1", "content", nil)

	// Only 1 shared tag (threshold is 2).
	insertTag(t, db, noteID, "go")
	insertTag(t, db, paperID, "go")

	engine := newTestEngine(db)
	count, err := engine.discoverTagCoOccurrence(ctx)
	if err != nil {
		t.Fatalf("discoverTagCoOccurrence: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 relationships for single shared tag, got %d", count)
	}
}

func TestDiscoverAuthorMatches(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	ghID := insertArtifact(t, db, "github", "repo", "jsmith/awesome-go", "jsmith/awesome-go",
		"A curated list", map[string]any{"owner": "jsmith"})
	paperID := insertArtifact(t, db, "arxiv", "paper", "arxiv:2403.99999", "Deep Learning for Go",
		"A study on Go programming", map[string]any{"authors": []string{"John Smith"}})

	_ = ghID
	_ = paperID

	engine := newTestEngine(db)
	count, err := engine.discoverAuthorMatches(ctx)
	if err != nil {
		t.Fatalf("discoverAuthorMatches: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 author match, got %d", count)
	}

	got := countRelationships(t, db, "AUTHORED_BY_SAME")
	if got != 1 {
		t.Errorf("expected 1 AUTHORED_BY_SAME relationship, got %d", got)
	}
}

func TestDiscoverAuthorMatches_NoMatch(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	insertArtifact(t, db, "github", "repo", "alice/project", "alice/project",
		"content", map[string]any{"owner": "alice"})
	insertArtifact(t, db, "arxiv", "paper", "arxiv:0001", "A Paper",
		"content", map[string]any{"authors": []string{"Bob Jones"}})

	engine := newTestEngine(db)
	count, err := engine.discoverAuthorMatches(ctx)
	if err != nil {
		t.Fatalf("discoverAuthorMatches: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 author matches for unrelated authors, got %d", count)
	}
}

func TestDiscoverCitationMatches(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	repoID := insertArtifact(t, db, "github", "repo", "openai/gpt-tools", "openai/gpt-tools",
		"GPT tools repo", nil)
	insertArtifact(t, db, "arxiv", "paper", "arxiv:2403.55555", "GPT Research",
		"We release code at https://github.com/openai/gpt-tools for reproducibility.",
		map[string]any{"authors": []string{"Research Team"}})

	_ = repoID

	engine := newTestEngine(db)
	count, err := engine.discoverCitationMatches(ctx)
	if err != nil {
		t.Fatalf("discoverCitationMatches: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 citation match, got %d", count)
	}

	got := countRelationships(t, db, "IMPLEMENTS")
	if got != 1 {
		t.Errorf("expected 1 IMPLEMENTS relationship, got %d", got)
	}
}

func TestDiscoverCitationMatches_NoGitHubURL(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	insertArtifact(t, db, "arxiv", "paper", "arxiv:0002", "Pure Theory",
		"This paper has no code links.", nil)

	engine := newTestEngine(db)
	count, err := engine.discoverCitationMatches(ctx)
	if err != nil {
		t.Fatalf("discoverCitationMatches: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 citation matches, got %d", count)
	}
}

func TestDiscoverTrendingResearch(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	trendingID := insertArtifact(t, db, "github_trending", "trending_repo", "trending:ml/transformers",
		"ml/transformers", "A transformer library for deep learning", nil)
	paperID := insertArtifact(t, db, "arxiv", "paper", "arxiv:2403.77777",
		"Transformer Architecture", "Attention mechanisms for deep learning", nil)

	// Use nearly identical embeddings so similarity exceeds threshold.
	vec := makeVec(0.3)
	insertEmbedding(t, db, trendingID, vec)
	insertEmbedding(t, db, paperID, makeSimilarVec(0.3, 0.005))

	engine := newTestEngine(db)
	count, err := engine.discoverTrendingResearch(ctx)
	if err != nil {
		t.Fatalf("discoverTrendingResearch: %v", err)
	}
	if count == 0 {
		t.Error("expected at least 1 trending-research relationship, got 0")
	}
}

func TestEngine_Run(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Set up artifacts for multiple strategies to fire.
	noteID := insertArtifact(t, db, "filesystem", "note", "note-run", "ML Notes", "Machine learning", nil)
	repoID := insertArtifact(t, db, "github", "repo", "testuser/ml-project", "testuser/ml-project",
		"ML project", map[string]any{"owner": "testuser"})
	paperID := insertArtifact(t, db, "arxiv", "paper", "arxiv:2403.00001", "ML Methods",
		"Machine learning methods. Code at https://github.com/testuser/ml-project.",
		map[string]any{"authors": []string{"Test User"}})

	vec := makeVec(0.4)
	insertEmbedding(t, db, noteID, vec)
	insertEmbedding(t, db, repoID, makeSimilarVec(0.4, 0.01))
	insertEmbedding(t, db, paperID, makeSimilarVec(0.4, 0.02))

	engine := newTestEngine(db)
	result, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Total == 0 {
		t.Error("expected some relationships, got 0 total")
	}
	if result.CitationMatches == 0 {
		t.Error("expected at least 1 citation match")
	}

	// Verify cursor was set.
	var cursorValue string
	err = db.QueryRow(`SELECT cursor_value FROM sync_cursors WHERE source_name = 'discovery:last_run'`).Scan(&cursorValue)
	if err != nil {
		t.Errorf("expected discovery cursor to be set: %v", err)
	}
}

func TestEngine_Run_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	repoID := insertArtifact(t, db, "github", "repo", "user/proj", "user/proj", "content", nil)
	insertArtifact(t, db, "arxiv", "paper", "arxiv:0003", "Paper",
		fmt.Sprintf("See https://github.com/user/proj for code"), nil)
	insertEmbedding(t, db, repoID, makeVec(0.5))

	engine := newTestEngine(db)

	result1, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}

	result2, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	// Second run should create 0 new relationships (all already exist).
	if result2.Total != 0 {
		t.Errorf("second run created %d new relationships, want 0 (first run created %d)", result2.Total, result1.Total)
	}
}
