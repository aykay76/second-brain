package trending

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"

	"pa/internal/config"
	"pa/internal/ingestion"
	"pa/internal/retrieval"
)

const (
	similarityThresholdImplements = 0.82
	similarityThresholdSimilar    = 0.78
	maxRelationshipCandidates     = 5
)

var arxivIDRe = regexp.MustCompile(`(?:arXiv:)?(\d{4}\.\d{4,5}(?:v\d+)?)`)

type Syncer struct {
	db         *sql.DB
	embedSvc   *retrieval.EmbeddingService
	scraper    *Scraper
	token      string
	cfg        config.TrendingConfig
	httpClient *http.Client
	apiBaseURL string
}

func NewSyncer(db *sql.DB, embedSvc *retrieval.EmbeddingService, cfg config.TrendingConfig, githubToken string) *Syncer {
	return &Syncer{
		db:         db,
		embedSvc:   embedSvc,
		scraper:    NewScraper(),
		token:      githubToken,
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBaseURL: "https://api.github.com",
	}
}

func (s *Syncer) Name() string { return "trending" }

func (s *Syncer) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	slog.Info("trending sync starting", "languages", s.cfg.Languages)

	repos, err := s.scraper.Scrape(ctx, s.cfg.Languages)
	if err != nil {
		return result, fmt.Errorf("scrape trending: %w", err)
	}

	slog.Info("trending repos scraped", "count", len(repos))

	syncDate := time.Now().UTC().Format(time.DateOnly)
	var syncedIDs []string

	for i := range repos {
		id, err := s.syncRepo(ctx, &repos[i], syncDate, result)
		if err != nil {
			slog.Error("failed to sync trending repo", "repo", repos[i].FullName, "error", err)
			result.Errors++
			continue
		}
		if id != "" {
			syncedIDs = append(syncedIDs, id)
		}
	}

	for _, id := range syncedIDs {
		s.detectImplements(ctx, id)
		s.detectSimilarTopic(ctx, id)
	}

	s.setCursor(ctx, "trending:sync", time.Now())

	slog.Info("trending sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

func (s *Syncer) syncRepo(ctx context.Context, repo *TrendingRepo, syncDate string, result *ingestion.SyncResult) (string, error) {
	content := repo.Description
	var topics []string

	if s.token != "" && s.cfg.FetchReadme {
		if info, err := s.fetchRepoInfo(ctx, repo.FullName); err == nil {
			topics = info.Topics
			if info.Description != "" && content == "" {
				content = info.Description
			}
		}

		if readme, err := s.fetchReadme(ctx, repo.FullName); err == nil && readme != "" {
			content += "\n\n" + readme
		}
	}

	hash := sha256Hash(fmt.Sprintf("%s:%d:%s", content, repo.StarsToday, syncDate))
	externalID := "trending:" + repo.FullName

	if s.isUnchanged(ctx, externalID, hash) {
		result.Skipped++
		return "", nil
	}

	metadata := map[string]any{
		"language":      repo.Language,
		"stars":         repo.Stars,
		"stars_today":   repo.StarsToday,
		"topics":        topics,
		"trending_date": syncDate,
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}

	var id string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata, content_hash, source_url, created_at)
		VALUES ('github_trending', 'trending_repo', $1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (source, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			content_hash = EXCLUDED.content_hash,
			updated_at = NOW()
		RETURNING id`,
		externalID, repo.Name, content, metadataJSON, hash, repo.HTMLURL,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert artifact: %w", err)
	}

	embeddingText := repo.Name + "\n" + content
	// Truncate to avoid exceeding embedding model context length
	const maxEmbeddingContentLen = 4000
	if len(embeddingText) > maxEmbeddingContentLen {
		embeddingText = embeddingText[:maxEmbeddingContentLen]
	}
	if err := s.embedSvc.EmbedArtifact(ctx, id, embeddingText); err != nil {
		slog.Warn("failed to generate embedding", "repo", repo.FullName, "error", err)
	}

	// Check for arXiv ID references in description/README content.
	s.detectImplementsFromText(ctx, id, content)

	result.Ingested++
	return id, nil
}

// detectImplementsFromText creates IMPLEMENTS relationships when the repo
// content explicitly references an arXiv paper ID.
func (s *Syncer) detectImplementsFromText(ctx context.Context, artifactID, content string) {
	matches := arxivIDRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return
	}

	seen := make(map[string]bool)
	for _, m := range matches {
		arxivID := m[1]
		if seen[arxivID] {
			continue
		}
		seen[arxivID] = true

		var targetID string
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM artifacts WHERE source = 'arxiv' AND external_id = $1`,
			"arxiv:"+arxivID,
		).Scan(&targetID)
		if err != nil {
			continue
		}

		s.createRelationship(ctx, artifactID, targetID, "IMPLEMENTS", 1.0, fmt.Sprintf(`{"arxiv_id": "%s", "method": "text_match"}`, arxivID))
	}
}

// detectImplements checks if a trending repo's embedding is semantically
// similar to arXiv papers, suggesting it implements ideas from research.
func (s *Syncer) detectImplements(ctx context.Context, artifactID string) {
	s.detectRelationship(ctx, artifactID, "arxiv", "paper", "IMPLEMENTS", similarityThresholdImplements)
}

// detectSimilarTopic checks if a trending repo is topically similar to the
// user's own GitHub repos.
func (s *Syncer) detectSimilarTopic(ctx context.Context, artifactID string) {
	s.detectRelationship(ctx, artifactID, "github", "repo", "SIMILAR_TOPIC", similarityThresholdSimilar)
}

func (s *Syncer) detectRelationship(ctx context.Context, artifactID, targetSource, targetType, relationType string, threshold float64) {
	var embedding pgvector.Vector
	err := s.db.QueryRowContext(ctx,
		`SELECT embedding FROM artifact_embeddings WHERE artifact_id = $1 LIMIT 1`,
		artifactID,
	).Scan(&embedding)
	if err != nil {
		return
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.title, 1 - (e.embedding <=> $1::vector) AS similarity
		FROM artifacts a
		JOIN artifact_embeddings e ON e.artifact_id = a.id
		WHERE a.source = $2 AND a.artifact_type = $3 AND a.id != $4
		ORDER BY e.embedding <=> $1::vector
		LIMIT $5`,
		&embedding, targetSource, targetType, artifactID, maxRelationshipCandidates,
	)
	if err != nil {
		slog.Warn("failed to search for related artifacts", "artifact_id", artifactID, "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var targetID, targetTitle string
		var similarity float64
		if err := rows.Scan(&targetID, &targetTitle, &similarity); err != nil {
			continue
		}
		if similarity < threshold {
			continue
		}

		meta := fmt.Sprintf(`{"similarity": %.4f, "method": "embedding"}`, similarity)
		s.createRelationship(ctx, artifactID, targetID, relationType, similarity, meta)

		slog.Info("relationship detected",
			"type", relationType,
			"target_title", targetTitle,
			"similarity", fmt.Sprintf("%.4f", similarity),
		)
	}
}

func (s *Syncer) createRelationship(ctx context.Context, sourceID, targetID, relationType string, confidence float64, metadata string) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO relationships (source_id, target_id, relation_type, confidence, metadata)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (source_id, target_id, relation_type) DO UPDATE SET
			confidence = EXCLUDED.confidence,
			metadata = EXCLUDED.metadata`,
		sourceID, targetID, relationType, confidence, metadata,
	)
	if err != nil {
		slog.Warn("failed to create relationship",
			"source", sourceID, "target", targetID, "type", relationType, "error", err)
	}
}

// --- GitHub API helpers ---

type repoInfo struct {
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
}

type readmeResponse struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

func (s *Syncer) fetchRepoInfo(ctx context.Context, fullName string) (*repoInfo, error) {
	var info repoInfo
	if err := s.apiGet(ctx, s.apiBaseURL+"/repos/"+fullName, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (s *Syncer) fetchReadme(ctx context.Context, fullName string) (string, error) {
	var readme readmeResponse
	if err := s.apiGet(ctx, s.apiBaseURL+"/repos/"+fullName+"/readme", &readme); err != nil {
		return "", err
	}
	if readme.Encoding == "base64" {
		cleaned := strings.ReplaceAll(readme.Content, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(cleaned)
		if err != nil {
			return "", fmt.Errorf("decode readme: %w", err)
		}
		return string(decoded), nil
	}
	return readme.Content, nil
}

func (s *Syncer) apiGet(ctx context.Context, url string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", scraperUserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api status %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

// --- DB helpers ---

func (s *Syncer) isUnchanged(ctx context.Context, externalID, hash string) bool {
	var existingHash sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM artifacts WHERE source = 'github_trending' AND external_id = $1`,
		externalID,
	).Scan(&existingHash)
	return err == nil && existingHash.Valid && existingHash.String == hash
}

func (s *Syncer) setCursor(ctx context.Context, name string, t time.Time) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_cursors (source_name, cursor_value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (source_name) DO UPDATE SET
			cursor_value = EXCLUDED.cursor_value,
			updated_at = NOW()`,
		name, t.Format(time.RFC3339Nano),
	)
	if err != nil {
		slog.Warn("failed to update sync cursor", "name", name, "error", err)
	}
}

func sha256Hash(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}
