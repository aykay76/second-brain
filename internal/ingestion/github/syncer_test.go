package github

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
		db.Exec("DELETE FROM relationships WHERE source_id IN (SELECT id FROM artifacts WHERE source = 'github')")
		db.Exec("DELETE FROM artifact_embeddings WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'github')")
		db.Exec("DELETE FROM tags WHERE artifact_id IN (SELECT id FROM artifacts WHERE source = 'github')")
		db.Exec("DELETE FROM artifacts WHERE source = 'github'")
		db.Exec("DELETE FROM sync_cursors WHERE source_name LIKE 'github:%'")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})
	return db
}

func newTestSyncer(t *testing.T, db *sql.DB, serverURL string, cfg config.GitHubConfig) *Syncer {
	t.Helper()
	embedSvc := retrieval.NewEmbeddingService(&stubEmbedder{}, db)
	s := NewSyncer(db, embedSvc, cfg)
	s.client.baseURL = serverURL
	return s
}

// --- Mock API helpers ---

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// --- Tests ---

func TestSyncer_SyncRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	readmeContent := "# My Repo\nSome documentation"
	readmeB64 := base64.StdEncoding.EncodeToString([]byte(readmeContent))

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghRepo{
			{
				FullName:    "testuser/myrepo",
				Name:        "myrepo",
				Description: "A test repository",
				Language:    "Go",
				Topics:      []string{"testing", "golang"},
				HTMLURL:     "https://github.com/testuser/myrepo",
				StarCount:   42,
				ForksCount:  5,
				Owner:       struct{ Login string `json:"login"` }{Login: "testuser"},
				CreatedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				PushedAt:    time.Now(),
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/readme", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, ghReadme{Content: readmeB64, Encoding: "base64"})
	})
	mux.HandleFunc("/repos/testuser/myrepo/pulls", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghPR{})
	})
	mux.HandleFunc("/repos/testuser/myrepo/commits", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghCommit{})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled: true, Token: "test-token",
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("ingested = %d, want 1", result.Ingested)
	}

	var title, content string
	err = db.QueryRowContext(ctx,
		"SELECT title, content FROM artifacts WHERE source = 'github' AND external_id = 'repo:testuser/myrepo'",
	).Scan(&title, &content)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if title != "myrepo" {
		t.Errorf("title = %q, want %q", title, "myrepo")
	}
	if content != "A test repository\n\n"+readmeContent {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestSyncer_SyncPRs(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghRepo{
			{
				FullName: "testuser/myrepo",
				Name:     "myrepo",
				HTMLURL:  "https://github.com/testuser/myrepo",
				Owner:    struct{ Login string `json:"login"` }{Login: "testuser"},
				PushedAt: time.Now(),
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/readme", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	})
	mux.HandleFunc("/repos/testuser/myrepo/pulls", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghPR{
			{
				Number:    1,
				Title:     "Add feature X",
				Body:      "This PR adds feature X.",
				State:     "merged",
				HTMLURL:   "https://github.com/testuser/myrepo/pull/1",
				Merged:    true,
				CreatedAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
				UpdatedAt: time.Now(),
				User:      struct{ Login string `json:"login"` }{Login: "testuser"},
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghComment{
			{
				Body:      "LGTM!",
				CreatedAt: time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC),
				User:      struct{ Login string `json:"login"` }{Login: "reviewer"},
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/commits", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghCommit{})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled: true, Token: "test-token",
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	var content string
	err = db.QueryRowContext(ctx,
		"SELECT content FROM artifacts WHERE source = 'github' AND external_id = 'pr:testuser/myrepo#1'",
	).Scan(&content)
	if err != nil {
		t.Fatalf("query PR artifact: %v", err)
	}
	if content == "" {
		t.Error("PR content is empty")
	}
	// PR should have 2 items: 1 repo + 1 PR
	if result.Ingested < 2 {
		t.Errorf("ingested = %d, want at least 2", result.Ingested)
	}
}

func TestSyncer_SyncCommits(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghRepo{
			{
				FullName: "testuser/myrepo",
				Name:     "myrepo",
				HTMLURL:  "https://github.com/testuser/myrepo",
				Owner:    struct{ Login string `json:"login"` }{Login: "testuser"},
				PushedAt: time.Now(),
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/readme", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	})
	mux.HandleFunc("/repos/testuser/myrepo/pulls", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghPR{})
	})
	mux.HandleFunc("/repos/testuser/myrepo/commits", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghCommit{
			{
				SHA:     "abc123def456",
				HTMLURL: "https://github.com/testuser/myrepo/commit/abc123def456",
				Commit: struct {
					Message string `json:"message"`
					Author  struct {
						Name  string    `json:"name"`
						Email string    `json:"email"`
						Date  time.Time `json:"date"`
					} `json:"author"`
				}{
					Message: "feat: add amazing feature\n\nDetailed description of the change.",
					Author: struct {
						Name  string    `json:"name"`
						Email string    `json:"email"`
						Date  time.Time `json:"date"`
					}{
						Name: "Test User", Email: "test@example.com",
						Date: time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC),
					},
				},
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/commits/abc123def456", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, ghCommitDetail{
			Stats: struct {
				Additions int `json:"additions"`
				Deletions int `json:"deletions"`
				Total     int `json:"total"`
			}{Additions: 50, Deletions: 10, Total: 3},
			Files: []struct {
				Filename  string `json:"filename"`
				Status    string `json:"status"`
				Additions int    `json:"additions"`
				Deletions int    `json:"deletions"`
			}{
				{Filename: "main.go", Status: "modified", Additions: 30, Deletions: 5},
				{Filename: "utils.go", Status: "added", Additions: 20, Deletions: 0},
				{Filename: "old.go", Status: "removed", Additions: 0, Deletions: 5},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled: true, Token: "test-token",
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	var title, content string
	err = db.QueryRowContext(ctx,
		"SELECT title, content FROM artifacts WHERE source = 'github' AND external_id = 'commit:testuser/myrepo:abc123def456'",
	).Scan(&title, &content)
	if err != nil {
		t.Fatalf("query commit artifact: %v", err)
	}
	if title != "feat: add amazing feature" {
		t.Errorf("title = %q, want %q", title, "feat: add amazing feature")
	}
	// 1 repo + 1 commit
	if result.Ingested < 2 {
		t.Errorf("ingested = %d, want at least 2", result.Ingested)
	}
}

func TestSyncer_SyncStarred(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	readmeContent := "# Awesome Lib"
	readmeB64 := base64.StdEncoding.EncodeToString([]byte(readmeContent))

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghRepo{})
	})
	mux.HandleFunc("/user/starred", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghStarredRepo{
			{
				StarredAt: time.Now().Format(time.RFC3339),
				Repo: ghRepo{
					FullName:    "cool/library",
					Name:        "library",
					Description: "A cool library",
					Language:    "Rust",
					Topics:      []string{"rust", "performance"},
					HTMLURL:     "https://github.com/cool/library",
					StarCount:   10000,
					Owner:       struct{ Login string `json:"login"` }{Login: "cool"},
					CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		})
	})
	mux.HandleFunc("/repos/cool/library/readme", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, ghReadme{Content: readmeB64, Encoding: "base64"})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled: true, Token: "test-token", SyncStarred: true,
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("ingested = %d, want 1", result.Ingested)
	}

	var artifactType string
	err = db.QueryRowContext(ctx,
		"SELECT artifact_type FROM artifacts WHERE source = 'github' AND external_id = 'star:cool/library'",
	).Scan(&artifactType)
	if err != nil {
		t.Fatalf("query starred artifact: %v", err)
	}
	if artifactType != "star" {
		t.Errorf("artifact_type = %q, want %q", artifactType, "star")
	}
}

func TestSyncer_SyncGists(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	gistID := "abc123gist"

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghRepo{})
	})
	mux.HandleFunc("/gists", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghGist{
			{
				ID:          gistID,
				Description: "My useful snippet",
				HTMLURL:     "https://gist.github.com/" + gistID,
				Public:      true,
				CreatedAt:   time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
				UpdatedAt:   time.Now(),
			},
		})
	})
	mux.HandleFunc("/gists/"+gistID, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, ghGist{
			ID:          gistID,
			Description: "My useful snippet",
			HTMLURL:     "https://gist.github.com/" + gistID,
			Public:      true,
			CreatedAt:   time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Now(),
			Files: map[string]struct {
				Filename string `json:"filename"`
				Language string `json:"language"`
				RawURL   string `json:"raw_url"`
				Size     int    `json:"size"`
				Content  string `json:"content"`
			}{
				"snippet.go": {
					Filename: "snippet.go",
					Language: "Go",
					Size:     42,
					Content:  "package main\n\nfunc main() {}",
				},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled: true, Token: "test-token", SyncGists: true,
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("ingested = %d, want 1", result.Ingested)
	}

	var title string
	err = db.QueryRowContext(ctx,
		"SELECT title FROM artifacts WHERE source = 'github' AND external_id = $1",
		"gist:"+gistID,
	).Scan(&title)
	if err != nil {
		t.Fatalf("query gist artifact: %v", err)
	}
	if title != "My useful snippet" {
		t.Errorf("title = %q, want %q", title, "My useful snippet")
	}
}

func TestSyncer_IncrementalSync(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		jsonResponse(w, []ghRepo{
			{
				FullName: "testuser/myrepo",
				Name:     "myrepo",
				HTMLURL:  "https://github.com/testuser/myrepo",
				Owner:    struct{ Login string `json:"login"` }{Login: "testuser"},
				PushedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/readme", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	})
	mux.HandleFunc("/repos/testuser/myrepo/pulls", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghPR{})
	})
	mux.HandleFunc("/repos/testuser/myrepo/commits", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghCommit{})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled: true, Token: "test-token",
	})

	// First sync
	result1, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if result1.Ingested != 1 {
		t.Errorf("first sync ingested = %d, want 1", result1.Ingested)
	}

	// Second sync — repo hasn't been pushed since cursor, should skip
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

func TestSyncer_IncludeRepos(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghRepo{})
	})
	mux.HandleFunc("/repos/golang/go", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, ghRepo{
			FullName:    "golang/go",
			Name:        "go",
			Description: "The Go programming language",
			Language:    "Go",
			HTMLURL:     "https://github.com/golang/go",
			StarCount:   120000,
			Owner:       struct{ Login string `json:"login"` }{Login: "golang"},
			PushedAt:    time.Now(),
		})
	})
	mux.HandleFunc("/repos/golang/go/readme", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	})
	mux.HandleFunc("/repos/golang/go/pulls", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghPR{})
	})
	mux.HandleFunc("/repos/golang/go/commits", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghCommit{})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled:      true,
		Token:        "test-token",
		IncludeRepos: []string{"golang/go"},
	})

	result, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("ingested = %d, want 1", result.Ingested)
	}

	var title string
	err = db.QueryRowContext(ctx,
		"SELECT title FROM artifacts WHERE source = 'github' AND external_id = 'repo:golang/go'",
	).Scan(&title)
	if err != nil {
		t.Fatalf("query included repo: %v", err)
	}
	if title != "go" {
		t.Errorf("title = %q, want %q", title, "go")
	}
}

func TestSyncer_CommitCrossReferences(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghRepo{
			{
				FullName: "testuser/myrepo",
				Name:     "myrepo",
				HTMLURL:  "https://github.com/testuser/myrepo",
				Owner:    struct{ Login string `json:"login"` }{Login: "testuser"},
				PushedAt: time.Now(),
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/readme", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	})
	mux.HandleFunc("/repos/testuser/myrepo/pulls", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghPR{
			{
				Number:    42,
				Title:     "Fix bug",
				Body:      "Fixes the bug",
				State:     "closed",
				HTMLURL:   "https://github.com/testuser/myrepo/pull/42",
				Merged:    true,
				CreatedAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
				UpdatedAt: time.Now(),
				User:      struct{ Login string `json:"login"` }{Login: "testuser"},
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghComment{})
	})
	mux.HandleFunc("/repos/testuser/myrepo/commits", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghCommit{
			{
				SHA:     "deadbeef1234",
				HTMLURL: "https://github.com/testuser/myrepo/commit/deadbeef1234",
				Commit: struct {
					Message string `json:"message"`
					Author  struct {
						Name  string    `json:"name"`
						Email string    `json:"email"`
						Date  time.Time `json:"date"`
					} `json:"author"`
				}{
					Message: "fix: resolve issue #42\n\nCloses #42",
					Author: struct {
						Name  string    `json:"name"`
						Email string    `json:"email"`
						Date  time.Time `json:"date"`
					}{
						Name: "Test User", Email: "test@example.com",
						Date: time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC),
					},
				},
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/commits/deadbeef1234", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, ghCommitDetail{})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled: true, Token: "test-token",
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var relCount int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM relationships WHERE relation_type = 'REFERENCES'",
	).Scan(&relCount)
	if err != nil {
		t.Fatalf("query relationships: %v", err)
	}
	if relCount == 0 {
		t.Error("expected at least one REFERENCES relationship from commit to PR")
	}
}

func TestSyncer_GeneratesEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghRepo{
			{
				FullName:    "testuser/myrepo",
				Name:        "myrepo",
				Description: "Some repo",
				HTMLURL:     "https://github.com/testuser/myrepo",
				Owner:       struct{ Login string `json:"login"` }{Login: "testuser"},
				PushedAt:    time.Now(),
			},
		})
	})
	mux.HandleFunc("/repos/testuser/myrepo/readme", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	})
	mux.HandleFunc("/repos/testuser/myrepo/pulls", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghPR{})
	})
	mux.HandleFunc("/repos/testuser/myrepo/commits", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, []ghCommit{})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	syncer := newTestSyncer(t, db, server.URL, config.GitHubConfig{
		Enabled: true, Token: "test-token",
	})

	if _, err := syncer.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var embeddingCount int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_embeddings ae
		JOIN artifacts a ON a.id = ae.artifact_id
		WHERE a.source = 'github'`,
	).Scan(&embeddingCount)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if embeddingCount == 0 {
		t.Error("expected at least one embedding for github artifacts")
	}
}

func TestSyncer_ImplementsSyncer(t *testing.T) {
	s := &Syncer{}
	if s.Name() != "github" {
		t.Errorf("Name() = %q, want %q", s.Name(), "github")
	}
}

func TestClient_Pagination(t *testing.T) {
	page := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/repos", func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos?page=2&per_page=100>; rel="next"`, "PLACEHOLDER"))
			jsonResponse(w, []ghRepo{{FullName: "repo1", Name: "repo1"}})
		} else {
			jsonResponse(w, []ghRepo{{FullName: "repo2", Name: "repo2"}})
		}
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()

	// Fix up the Link header to use the actual server URL
	origHandler := mux
	fixedMux := http.NewServeMux()
	fixedMux.HandleFunc("/repos", func(w http.ResponseWriter, r *http.Request) {
		page2 := r.URL.Query().Get("page")
		if page2 == "2" {
			jsonResponse(w, []ghRepo{{FullName: "repo2", Name: "repo2"}})
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos?page=2&per_page=100>; rel="next"`, server.URL))
		jsonResponse(w, []ghRepo{{FullName: "repo1", Name: "repo1"}})
	})
	_ = origHandler

	server.Config.Handler = fixedMux

	client := NewClient("test-token")
	client.baseURL = server.URL

	ctx := context.Background()
	repos, err := getPaginated[ghRepo](ctx, client, "/repos")
	if err != nil {
		t.Fatalf("getPaginated: %v", err)
	}
	if len(repos) != 2 {
		t.Errorf("got %d repos, want 2", len(repos))
	}
}

func TestExtractNextLink(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{
			header: `<https://api.github.com/user/repos?page=2>; rel="next", <https://api.github.com/user/repos?page=5>; rel="last"`,
			want:   "https://api.github.com/user/repos?page=2",
		},
		{
			header: `<https://api.github.com/user/repos?page=5>; rel="last"`,
			want:   "",
		},
		{
			header: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		got := extractNextLink(tt.header)
		if got != tt.want {
			t.Errorf("extractNextLink(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestIssueRefRegex(t *testing.T) {
	tests := []struct {
		message string
		want    int
	}{
		{"fix: resolve #42", 1},
		{"closes #42, fixes #43", 2},
		{"ref other/repo#10", 1},
		{"no refs here", 0},
		{"fix: #1 and other/repo#2", 2},
	}

	for _, tt := range tests {
		matches := issueRefRe.FindAllStringSubmatch(tt.message, -1)
		if len(matches) != tt.want {
			t.Errorf("message %q: got %d matches, want %d", tt.message, len(matches), tt.want)
		}
	}
}
