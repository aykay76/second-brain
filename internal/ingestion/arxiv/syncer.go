package arxiv

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"pa/internal/config"
	"pa/internal/ingestion"
	"pa/internal/retrieval"
)

const sourcePrefix = "arxiv:"

var arxivIDRe = regexp.MustCompile(`(?:arXiv:)?(\d{4}\.\d{4,5}(?:v\d+)?)`)

type Syncer struct {
	db       *sql.DB
	embedSvc *retrieval.EmbeddingService
	client   *Client
	cfg      config.ArXivConfig
}

func NewSyncer(db *sql.DB, embedSvc *retrieval.EmbeddingService, cfg config.ArXivConfig) *Syncer {
	return &Syncer{
		db:       db,
		embedSvc: embedSvc,
		client:   NewClient(),
		cfg:      cfg,
	}
}

func (s *Syncer) Name() string { return "arxiv" }

func (s *Syncer) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	from := s.resolveStartDate(ctx)
	to := time.Now().UTC()

	slog.Info("arxiv sync starting",
		"categories", s.cfg.Categories,
		"keywords", s.cfg.Keywords,
		"from", from.Format(time.DateOnly),
		"to", to.Format(time.DateOnly),
	)

	maxResults := s.cfg.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

	papers, err := s.client.Search(ctx, s.cfg.Categories, s.cfg.Keywords, from, to, maxResults)
	if err != nil {
		return result, fmt.Errorf("arxiv search: %w", err)
	}

	slog.Info("arxiv papers fetched", "count", len(papers))

	var latestPublished time.Time
	for i := range papers {
		if err := s.syncPaper(ctx, &papers[i], result); err != nil {
			slog.Error("failed to sync paper", "arxiv_id", papers[i].ArXivID, "error", err)
			result.Errors++
			continue
		}
		if papers[i].Published.After(latestPublished) {
			latestPublished = papers[i].Published
		}
	}

	// Extract CITES relationships after all papers are ingested so cross-references resolve.
	for i := range papers {
		s.extractCitations(ctx, &papers[i])
	}

	if !latestPublished.IsZero() {
		s.setCursor(ctx, "arxiv:sync", latestPublished)
	}

	slog.Info("arxiv sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

func (s *Syncer) resolveStartDate(ctx context.Context) time.Time {
	cursor := s.getCursor(ctx, "arxiv:sync")
	if !cursor.IsZero() {
		return cursor
	}

	lookback := parseDuration(s.cfg.InitialLookback, 30*24*time.Hour)
	return time.Now().UTC().Add(-lookback)
}

func (s *Syncer) syncPaper(ctx context.Context, paper *Paper, result *ingestion.SyncResult) error {
	content := paper.Abstract
	hash := sha256Hash(content)
	externalID := sourcePrefix + paper.ArXivID

	if s.isUnchanged(ctx, externalID, hash) {
		result.Skipped++
		return nil
	}

	metadata := map[string]any{
		"authors":    paper.Authors,
		"categories": paper.Categories,
		"arxiv_id":   paper.ArXivID,
		"pdf_url":    paper.PDFURL,
	}
	if !paper.Updated.IsZero() {
		metadata["updated"] = paper.Updated.Format(time.RFC3339)
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	var id string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata, content_hash, source_url, created_at)
		VALUES ('arxiv', 'paper', $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			content_hash = EXCLUDED.content_hash,
			updated_at = NOW()
		RETURNING id`,
		externalID, paper.Title, content, metadataJSON, hash, paper.AbstractURL, paper.Published,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("upsert artifact: %w", err)
	}

	embeddingText := paper.Title + "\n" + paper.Abstract
	if err := s.embedSvc.EmbedArtifact(ctx, id, embeddingText); err != nil {
		slog.Warn("failed to generate embedding", "arxiv_id", paper.ArXivID, "error", err)
	}

	result.Ingested++
	return nil
}

// extractCitations scans an abstract for arXiv ID references and creates
// CITES relationships to papers already in the database.
func (s *Syncer) extractCitations(ctx context.Context, paper *Paper) {
	matches := arxivIDRe.FindAllStringSubmatch(paper.Abstract, -1)
	if len(matches) == 0 {
		return
	}

	sourceID, err := s.getArtifactID(ctx, sourcePrefix+paper.ArXivID)
	if err != nil {
		return
	}

	seen := make(map[string]bool)
	for _, m := range matches {
		citedID := normaliseArXivID(m[1])
		if citedID == normaliseArXivID(paper.ArXivID) || seen[citedID] {
			continue
		}
		seen[citedID] = true

		targetID, err := s.getArtifactID(ctx, sourcePrefix+citedID)
		if err != nil {
			// Also try versioned variants: if the reference is unversioned,
			// we won't find it if stored with version, and vice-versa.
			continue
		}

		_, err = s.db.ExecContext(ctx, `
			INSERT INTO relationships (source_id, target_id, relation_type, confidence, metadata)
			VALUES ($1, $2, 'CITES', 1.0, $3)
			ON CONFLICT (source_id, target_id, relation_type) DO NOTHING`,
			sourceID, targetID, fmt.Sprintf(`{"cited_arxiv_id": "%s"}`, citedID),
		)
		if err != nil {
			slog.Warn("failed to create citation relationship",
				"from", paper.ArXivID, "to", citedID, "error", err)
		}
	}
}

// normaliseArXivID strips a version suffix (e.g. "v1") to allow matching
// across versions.
func normaliseArXivID(id string) string {
	if idx := strings.LastIndex(id, "v"); idx > 0 {
		suffix := id[idx+1:]
		allDigits := true
		for _, c := range suffix {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits && len(suffix) > 0 {
			return id[:idx]
		}
	}
	return id
}

// --- helpers ---

func (s *Syncer) isUnchanged(ctx context.Context, externalID, hash string) bool {
	var existingHash sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM artifacts WHERE source = 'arxiv' AND external_id = $1`,
		externalID,
	).Scan(&existingHash)
	return err == nil && existingHash.Valid && existingHash.String == hash
}

func (s *Syncer) getArtifactID(ctx context.Context, externalID string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM artifacts WHERE source = 'arxiv' AND external_id = $1`, externalID,
	).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Syncer) getCursor(ctx context.Context, name string) time.Time {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor_value FROM sync_cursors WHERE source_name = $1`, name,
	).Scan(&val)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, val)
	if err != nil {
		return time.Time{}
	}
	return t
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

// parseDuration parses a human-friendly duration string like "30d", "7d", "2w".
// Falls back to the provided default on parse failure.
func parseDuration(s string, fallback time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}

	multiplier := time.Hour * 24
	suffix := s[len(s)-1]
	switch suffix {
	case 'd', 'D':
		s = s[:len(s)-1]
	case 'w', 'W':
		multiplier = time.Hour * 24 * 7
		s = s[:len(s)-1]
	default:
		d, err := time.ParseDuration(s)
		if err != nil {
			return fallback
		}
		return d
	}

	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return fallback
	}
	return time.Duration(n) * multiplier
}
