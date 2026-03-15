package thenewstack

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
	cfg      config.TheNewStackConfig
}

func NewSyncer(db *sql.DB, embedSvc *retrieval.EmbeddingService, cfg config.TheNewStackConfig) *Syncer {
	return &Syncer{
		db:       db,
		embedSvc: embedSvc,
		client:   NewClient(),
		cfg:      cfg,
	}
}

func (s *Syncer) Name() string { return "thenewstack" }

func (s *Syncer) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	if !s.cfg.Enabled {
		slog.Debug("thenewstack syncer disabled")
		return result, nil
	}

	slog.Info("thenewstack sync starting", "limit", s.cfg.MaxArticles)

	articles, err := s.client.ScrapeLatestArticles(ctx, s.cfg.MaxArticles)
	if err != nil {
		return result, fmt.Errorf("scrape thenewstack: %w", err)
	}

	slog.Info("thenewstack articles scraped", "count", len(articles))

	for i := range articles {
		if err := s.syncArticle(ctx, &articles[i], result); err != nil {
			slog.Error("failed to sync thenewstack article", "url", articles[i].URL, "error", err)
			result.Errors++
			continue
		}
	}

	s.setCursor(ctx, "thenewstack:sync", time.Now())

	slog.Info("thenewstack sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

func (s *Syncer) syncArticle(ctx context.Context, article *Article, result *ingestion.SyncResult) error {
	// Skip articles without title or URL
	if article.Title == "" || article.URL == "" {
		result.Skipped++
		return nil
	}

	// Create content - combine title, description, and categories
	content := article.Title
	if article.Description != "" {
		content += "\n\n" + article.Description
	}

	// Build metadata
	metadata := map[string]any{
		"url":          article.URL,
		"authors":      article.Authors,
		"categories":   article.Categories,
		"published_at": article.PublishedAt.Format(time.RFC3339),
	}

	// Compute content hash for deduplication
	hash := sha256Hash(article.URL + ":" + article.Title)
	externalID := "thenewstack:" + extractArticleID(article.URL)

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
		VALUES ('thenewstack', 'article', $1, $2, $3, $4, $5, $6, NOW())
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
		`SELECT content_hash FROM artifacts WHERE source = 'thenewstack' AND external_id = $1 LIMIT 1`,
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

// setCursor stores the last sync time
func (s *Syncer) setCursor(ctx context.Context, key string, value time.Time) {
	// This stores metadata about the last sync - useful for incremental syncing
	query := `
		INSERT INTO metadata (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`
	if err := s.db.QueryRowContext(ctx, query, key, value.Format(time.RFC3339)).Err(); err != nil {
		slog.Warn("failed to set cursor", "key", key, "error", err)
	}
}

// sha256Hash computes SHA256 hash of a string
func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// extractArticleID extracts a unique ID from the article URL
func extractArticleID(url string) string {
	// Remove protocol and domain
	if idx := strings.Index(url, "://"); idx != -1 {
		url = url[idx+3:]
	}
	if idx := strings.Index(url, "/"); idx != -1 {
		url = url[idx+1:]
	}
	// Remove trailing slash
	url = strings.TrimSuffix(url, "/")
	// Return slug or last part of URL
	parts := strings.Split(url, "/")
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		return parts[len(parts)-1]
	}
	return url
}
