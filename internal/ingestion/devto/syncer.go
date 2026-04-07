package devto

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"pa/internal/config"
	"pa/internal/ingestion"
	"pa/internal/retrieval"
)

type Syncer struct {
	db       *sql.DB
	embedSvc *retrieval.EmbeddingService
	client   *Client
	cfg      config.DevToConfig
}

func NewSyncer(db *sql.DB, embedSvc *retrieval.EmbeddingService, cfg config.DevToConfig) *Syncer {
	return &Syncer{
		db:       db,
		embedSvc: embedSvc,
		client:   NewClient(),
		cfg:      cfg,
	}
}

func (s *Syncer) Name() string { return "devto" }

func (s *Syncer) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	if !s.cfg.Enabled {
		slog.Debug("devto syncer disabled")
		return result, nil
	}

	slog.Info("devto sync starting", "tags", len(s.cfg.Tags), "max_articles", s.cfg.MaxArticles)

	maxArticles := s.cfg.MaxArticles
	if maxArticles <= 0 {
		maxArticles = 50
	}

	// Fetch articles for each tag
	for _, tag := range s.cfg.Tags {
		if err := s.syncTag(ctx, tag, maxArticles, result); err != nil {
			slog.Error("failed to sync devto tag", "tag", tag, "error", err)
			result.Errors++
			continue
		}
	}

	slog.Info("devto sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

func (s *Syncer) syncTag(ctx context.Context, tag string, maxArticles int, result *ingestion.SyncResult) error {
	var allArticles []Article
	perPage := 30
	page := 1
	totalFetched := 0

	// Paginate through results until we have enough or API returns no more
	for totalFetched < maxArticles {
		articles, err := s.client.FetchArticlesByTag(ctx, tag, page, perPage)
		if err != nil {
			return fmt.Errorf("fetch articles for tag %s: %w", tag, err)
		}

		if len(articles) == 0 {
			break
		}

		allArticles = append(allArticles, articles...)
		totalFetched += len(articles)
		page++
	}

	slog.Info("devto articles fetched", "tag", tag, "count", len(allArticles))

	// Sync each article
	for i := range allArticles {
		if totalFetched > maxArticles && i >= maxArticles {
			break
		}

		if err := s.syncArticle(ctx, &allArticles[i], result); err != nil {
			slog.Error("failed to sync devto article", "url", allArticles[i].URL, "error", err)
			result.Errors++
			continue
		}
	}

	return nil
}

func (s *Syncer) syncArticle(ctx context.Context, article *Article, result *ingestion.SyncResult) error {
	// Skip articles without title or URL
	if article.Title == "" || article.URL == "" {
		result.Skipped++
		return nil
	}

	// Skip if not published
	if !article.Published {
		result.Skipped++
		return nil
	}

	// Use body_markdown for content if available, otherwise body_html
	content := article.Title
	if article.BodyMarkdown != "" {
		content += "\n\n" + article.BodyMarkdown
	} else if article.BodyHTML != "" {
		content += "\n\n" + article.BodyHTML
	}

	// Build metadata
	metadata := map[string]any{
		"url":          article.URL,
		"author":       article.Author.Name,
		"author_id":    article.AuthorID,
		"tags":         article.Tags,
		"published_at": article.PublishedAt.Format(time.RFC3339),
		"reading_time": article.ReadingTimeMin,
		"cover_image":  article.CoverURL,
	}

	// Compute content hash for deduplication
	hash := sha256Hash(article.URL + ":" + article.Title)
	externalID := fmt.Sprintf("devto:article:%d", article.ID)

	// Check if article already exists and is unchanged
	if s.isUnchanged(ctx, externalID, hash) {
		result.Skipped++
		return nil
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	// Insert or update article in database
	var id string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata, content_hash, source_url, created_at)
		VALUES ('devto', 'article', $1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (source, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			content_hash = EXCLUDED.content_hash,
			updated_at = NOW()
		RETURNING id`,
		externalID, article.Title, content, metadataJSON, hash, article.URL,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("insert article: %w", err)
	}

	result.Ingested++
	return nil
}

// isUnchanged checks if an article with the same hash already exists
func (s *Syncer) isUnchanged(ctx context.Context, externalID, hash string) bool {
	var storedHash string
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM artifacts WHERE source = 'devto' AND external_id = $1 LIMIT 1`,
		externalID,
	).Scan(&storedHash)

	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		slog.Error("failed to check article hash", "error", err)
		return false
	}

	return storedHash == hash
}

// Helper functions

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func extractArticleID(url string) string {
	// Remove query parameters
	if idx := strings.Index(url, "?"); idx != -1 {
		url = url[:idx]
	}

	// Get the last part of the path
	parts := strings.Split(strings.TrimSuffix(url, "/"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func timeStringEmpty(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse(time.RFC3339, s)
	return err != nil
}
