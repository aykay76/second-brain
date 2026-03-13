package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"

	"pa/internal/config"
)

const (
	thresholdRelatedTo    = 0.80
	thresholdImplements   = 0.82
	thresholdSimilarTopic = 0.78
)

var githubURLRe = regexp.MustCompile(`https?://github\.com/([\w.\-]+/[\w.\-]+)`)

type Engine struct {
	db  *sql.DB
	cfg config.DiscoveryConfig
}

func NewEngine(db *sql.DB, cfg config.DiscoveryConfig) *Engine {
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = 10
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.SimilarityThreshold <= 0 {
		cfg.SimilarityThreshold = thresholdRelatedTo
	}
	return &Engine{db: db, cfg: cfg}
}

type Result struct {
	CrossSourceRelated int `json:"cross_source_related"`
	TagCoOccurrence    int `json:"tag_co_occurrence"`
	AuthorMatches      int `json:"author_matches"`
	CitationMatches    int `json:"citation_matches"`
	TrendingResearch   int `json:"trending_research"`
	Total              int `json:"total"`
}

func (e *Engine) Run(ctx context.Context) (*Result, error) {
	start := time.Now()
	slog.Info("discovery engine starting")

	result := &Result{}

	strategies := []struct {
		name string
		fn   func(context.Context) (int, error)
		dest *int
	}{
		{"cross-source similarity", e.discoverCrossSourceSimilarity, &result.CrossSourceRelated},
		{"tag co-occurrence", e.discoverTagCoOccurrence, &result.TagCoOccurrence},
		{"author matching", e.discoverAuthorMatches, &result.AuthorMatches},
		{"citation matching", e.discoverCitationMatches, &result.CitationMatches},
		{"trending-research linking", e.discoverTrendingResearch, &result.TrendingResearch},
	}

	for _, s := range strategies {
		n, err := s.fn(ctx)
		if err != nil {
			slog.Error("discovery strategy failed", "strategy", s.name, "error", err)
			continue
		}
		*s.dest = n
		result.Total += n
		slog.Info("discovery strategy complete", "strategy", s.name, "relationships", n)
	}

	e.setCursor(ctx, "discovery:last_run", time.Now())

	slog.Info("discovery engine complete",
		"total_relationships", result.Total,
		"duration", time.Since(start).Round(time.Millisecond),
	)
	return result, nil
}

// discoverCrossSourceSimilarity finds semantically similar artifacts across
// different sources and creates RELATED_TO relationships.
func (e *Engine) discoverCrossSourceSimilarity(ctx context.Context) (int, error) {
	count := 0
	offset := 0

	for {
		batch, err := e.loadArtifactBatch(ctx, offset)
		if err != nil {
			return count, fmt.Errorf("load batch at offset %d: %w", offset, err)
		}
		if len(batch) == 0 {
			break
		}

		for _, a := range batch {
			n, err := e.findCrossSourceNeighbors(ctx, a.id, a.source, a.embedding)
			if err != nil {
				slog.Warn("cross-source scan failed", "artifact_id", a.id, "error", err)
				continue
			}
			count += n
		}

		offset += e.cfg.BatchSize
	}

	return count, nil
}

type artifactWithEmbedding struct {
	id        string
	source    string
	embedding pgvector.Vector
}

func (e *Engine) loadArtifactBatch(ctx context.Context, offset int) ([]artifactWithEmbedding, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT a.id, a.source, e.embedding
		FROM artifacts a
		JOIN artifact_embeddings e ON e.artifact_id = a.id
		ORDER BY a.id
		LIMIT $1 OFFSET $2
	`, e.cfg.BatchSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var batch []artifactWithEmbedding
	for rows.Next() {
		var a artifactWithEmbedding
		if err := rows.Scan(&a.id, &a.source, &a.embedding); err != nil {
			return nil, err
		}
		batch = append(batch, a)
	}
	return batch, rows.Err()
}

func (e *Engine) findCrossSourceNeighbors(ctx context.Context, id, source string, embedding pgvector.Vector) (int, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT a.id, a.title, a.source,
			1 - (e.embedding <=> $1::vector) AS similarity
		FROM artifacts a
		JOIN artifact_embeddings e ON e.artifact_id = a.id
		WHERE a.source != $2 AND a.id != $3
		ORDER BY e.embedding <=> $1::vector
		LIMIT $4
	`, &embedding, source, id, e.cfg.MaxCandidates)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var targetID, targetTitle, targetSource string
		var similarity float64
		if err := rows.Scan(&targetID, &targetTitle, &targetSource, &similarity); err != nil {
			continue
		}
		if similarity < e.cfg.SimilarityThreshold {
			continue
		}

		meta := fmt.Sprintf(`{"similarity": %.4f, "method": "embedding", "sources": ["%s", "%s"]}`,
			similarity, source, targetSource)
		if e.upsertRelationship(ctx, id, targetID, "RELATED_TO", similarity, meta) {
			count++
		}
	}
	return count, rows.Err()
}

// discoverTagCoOccurrence creates SIMILAR_TOPIC relationships between
// artifacts that share tags.
func (e *Engine) discoverTagCoOccurrence(ctx context.Context) (int, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT t1.artifact_id, t2.artifact_id, array_agg(DISTINCT t1.tag) AS shared_tags, count(DISTINCT t1.tag) AS tag_count
		FROM tags t1
		JOIN tags t2 ON t1.tag = t2.tag AND t1.artifact_id < t2.artifact_id
		JOIN artifacts a1 ON a1.id = t1.artifact_id
		JOIN artifacts a2 ON a2.id = t2.artifact_id
		WHERE a1.source != a2.source
		GROUP BY t1.artifact_id, t2.artifact_id
		HAVING count(DISTINCT t1.tag) >= 2
	`)
	if err != nil {
		return 0, fmt.Errorf("tag co-occurrence query: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var sourceID, targetID string
		var sharedTags []string
		var tagCount int
		if err := rows.Scan(&sourceID, &targetID, pqStringArray(&sharedTags), &tagCount); err != nil {
			slog.Warn("scan tag co-occurrence", "error", err)
			continue
		}

		confidence := min(float64(tagCount)/5.0, 1.0)
		tagsJSON, _ := json.Marshal(sharedTags)
		meta := fmt.Sprintf(`{"shared_tags": %s, "method": "tag_co_occurrence"}`, tagsJSON)
		if e.upsertRelationship(ctx, sourceID, targetID, "SIMILAR_TOPIC", confidence, meta) {
			count++
		}
	}
	return count, rows.Err()
}

// discoverAuthorMatches finds artifacts by the same author across different
// sources and creates AUTHORED_BY_SAME relationships.
func (e *Engine) discoverAuthorMatches(ctx context.Context) (int, error) {
	count := 0

	n, err := e.matchArxivToGitHub(ctx)
	if err != nil {
		slog.Warn("arxiv-github author matching failed", "error", err)
	}
	count += n

	n, err = e.matchYouTubeToGitHub(ctx)
	if err != nil {
		slog.Warn("youtube-github author matching failed", "error", err)
	}
	count += n

	return count, nil
}

type authorEntry struct {
	id      string
	authors []string
}

// matchArxivToGitHub matches arXiv paper authors to GitHub repo owners.
func (e *Engine) matchArxivToGitHub(ctx context.Context) (int, error) {
	arxivArtifacts, err := e.loadArxivAuthors(ctx)
	if err != nil {
		return 0, err
	}

	ghOwners, err := e.loadGitHubOwners(ctx)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, paper := range arxivArtifacts {
		for _, author := range paper.authors {
			count += e.matchAuthorToOwners(ctx, paper.id, author, ghOwners, 0.8)
		}
	}
	return count, nil
}

func (e *Engine) loadArxivAuthors(ctx context.Context) ([]authorEntry, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, metadata->>'authors' FROM artifacts WHERE source = 'arxiv'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []authorEntry
	for rows.Next() {
		var id string
		var authorsJSON sql.NullString
		if err := rows.Scan(&id, &authorsJSON); err != nil {
			continue
		}
		if !authorsJSON.Valid || authorsJSON.String == "" {
			continue
		}
		var authors []string
		if err := json.Unmarshal([]byte(authorsJSON.String), &authors); err != nil {
			continue
		}
		if len(authors) > 0 {
			entries = append(entries, authorEntry{id: id, authors: authors})
		}
	}
	return entries, rows.Err()
}

func (e *Engine) matchAuthorToOwners(ctx context.Context, artifactID, author string, owners map[string][]string, confidence float64) int {
	normalised := normaliseAuthorName(author)
	count := 0
	for ownerName, ownerIDs := range owners {
		if !authorNameMatch(normalised, ownerName) {
			continue
		}
		for _, ghID := range ownerIDs {
			meta := fmt.Sprintf(`{"author": %q, "github_owner": %q, "method": "author_name"}`,
				author, ownerName)
			if e.upsertRelationship(ctx, artifactID, ghID, "AUTHORED_BY_SAME", confidence, meta) {
				count++
			}
		}
	}
	return count
}

// matchYouTubeToGitHub matches YouTube channel names to GitHub owners.
func (e *Engine) matchYouTubeToGitHub(ctx context.Context) (int, error) {
	ytRows, err := e.db.QueryContext(ctx, `
		SELECT id, metadata->>'channel' FROM artifacts WHERE source = 'youtube'
	`)
	if err != nil {
		return 0, err
	}
	defer ytRows.Close()

	type channelEntry struct {
		id      string
		channel string
	}
	var ytArtifacts []channelEntry

	for ytRows.Next() {
		var id string
		var channel sql.NullString
		if err := ytRows.Scan(&id, &channel); err != nil {
			continue
		}
		if channel.Valid && channel.String != "" {
			ytArtifacts = append(ytArtifacts, channelEntry{id: id, channel: channel.String})
		}
	}
	if err := ytRows.Err(); err != nil {
		return 0, err
	}

	ghOwners, err := e.loadGitHubOwners(ctx)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, video := range ytArtifacts {
		count += e.matchAuthorToOwners(ctx, video.id, video.channel, ghOwners, 0.75)
	}
	return count, nil
}

func (e *Engine) loadGitHubOwners(ctx context.Context) (map[string][]string, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, title, metadata->>'owner' FROM artifacts
		WHERE source = 'github' AND artifact_type = 'repo'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	owners := make(map[string][]string)
	for rows.Next() {
		var id, title string
		var owner sql.NullString
		if err := rows.Scan(&id, &title, &owner); err != nil {
			continue
		}
		name := ""
		if owner.Valid && owner.String != "" {
			name = strings.ToLower(owner.String)
		} else if parts := strings.SplitN(title, "/", 2); len(parts) == 2 {
			name = strings.ToLower(parts[0])
		}
		if name != "" {
			owners[name] = append(owners[name], id)
		}
	}
	return owners, rows.Err()
}

// discoverCitationMatches scans arXiv paper content for GitHub repo URLs
// and creates IMPLEMENTS relationships.
func (e *Engine) discoverCitationMatches(ctx context.Context) (int, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, content FROM artifacts
		WHERE source = 'arxiv' AND content IS NOT NULL AND content != ''
	`)
	if err != nil {
		return 0, fmt.Errorf("query arxiv content: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			continue
		}
		count += e.matchGitHubURLsInContent(ctx, id, content)
	}
	return count, rows.Err()
}

func (e *Engine) matchGitHubURLsInContent(ctx context.Context, paperID, content string) int {
	matches := githubURLRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return 0
	}

	count := 0
	seen := make(map[string]bool)
	for _, m := range matches {
		repoPath := strings.ToLower(m[1])
			repoPath = strings.TrimRight(repoPath, ".")
			repoPath = strings.TrimSuffix(repoPath, ".git")
		if seen[repoPath] {
			continue
		}
		seen[repoPath] = true

		var targetID string
		err := e.db.QueryRowContext(ctx,
			`SELECT id FROM artifacts
			 WHERE source IN ('github', 'github_trending')
			   AND (lower(external_id) = $1 OR lower(title) = $2 OR lower(external_id) LIKE '%' || $1)`,
			repoPath, repoPath,
		).Scan(&targetID)
		if err != nil {
			continue
		}

		meta := fmt.Sprintf(`{"github_url": "https://github.com/%s", "method": "url_citation"}`, repoPath)
		if e.upsertRelationship(ctx, targetID, paperID, "IMPLEMENTS", 1.0, meta) {
			count++
			slog.Info("citation match found", "paper_id", paperID, "repo", repoPath)
		}
	}
	return count
}

// discoverTrendingResearch finds trending repos that are semantically similar
// to arXiv papers and creates IMPLEMENTS relationships.
func (e *Engine) discoverTrendingResearch(ctx context.Context) (int, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT a.id, e.embedding
		FROM artifacts a
		JOIN artifact_embeddings e ON e.artifact_id = a.id
		WHERE a.source = 'github_trending'
	`)
	if err != nil {
		return 0, fmt.Errorf("query trending embeddings: %w", err)
	}
	defer rows.Close()

	type trendingArtifact struct {
		id        string
		embedding pgvector.Vector
	}
	var trending []trendingArtifact
	for rows.Next() {
		var t trendingArtifact
		if err := rows.Scan(&t.id, &t.embedding); err != nil {
			continue
		}
		trending = append(trending, t)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	count := 0
	for _, t := range trending {
		n, err := e.matchTrendingToPapers(ctx, t.id, t.embedding)
		if err != nil {
			slog.Warn("trending-research query failed", "trending_id", t.id, "error", err)
			continue
		}
		count += n
	}
	return count, nil
}

func (e *Engine) matchTrendingToPapers(ctx context.Context, trendingID string, embedding pgvector.Vector) (int, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT a.id, 1 - (e.embedding <=> $1::vector) AS similarity
		FROM artifacts a
		JOIN artifact_embeddings e ON e.artifact_id = a.id
		WHERE a.source = 'arxiv' AND a.id != $2
		ORDER BY e.embedding <=> $1::vector
		LIMIT $3
	`, &embedding, trendingID, e.cfg.MaxCandidates)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var paperID string
		var similarity float64
		if err := rows.Scan(&paperID, &similarity); err != nil {
			continue
		}
		if similarity < thresholdImplements {
			continue
		}
		meta := fmt.Sprintf(`{"similarity": %.4f, "method": "embedding"}`, similarity)
		if e.upsertRelationship(ctx, trendingID, paperID, "IMPLEMENTS", similarity, meta) {
			count++
		}
	}
	return count, rows.Err()
}

// upsertRelationship creates or updates a relationship, returning true if a new row was inserted.
func (e *Engine) upsertRelationship(ctx context.Context, sourceID, targetID, relationType string, confidence float64, metadata string) bool {
	var inserted bool
	err := e.db.QueryRowContext(ctx, `
		INSERT INTO relationships (source_id, target_id, relation_type, confidence, metadata)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		ON CONFLICT (source_id, target_id, relation_type) DO UPDATE SET
			confidence = GREATEST(relationships.confidence, EXCLUDED.confidence),
			metadata = EXCLUDED.metadata
		RETURNING (xmax = 0)
	`, sourceID, targetID, relationType, confidence, metadata).Scan(&inserted)
	if err != nil {
		slog.Warn("failed to upsert relationship",
			"source", sourceID, "target", targetID, "type", relationType, "error", err)
		return false
	}
	return inserted
}

func (e *Engine) setCursor(ctx context.Context, name string, t time.Time) {
	_, err := e.db.ExecContext(ctx, `
		INSERT INTO sync_cursors (source_name, cursor_value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (source_name) DO UPDATE SET
			cursor_value = EXCLUDED.cursor_value,
			updated_at = NOW()`,
		name, t.Format(time.RFC3339Nano),
	)
	if err != nil {
		slog.Warn("failed to update discovery cursor", "name", name, "error", err)
	}
}

// normaliseAuthorName lowercases and strips common suffixes/prefixes for matching.
func normaliseAuthorName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, ".", " ")
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	return strings.Join(strings.Fields(name), " ")
}

// authorNameMatch checks whether two normalised names are likely the same person.
// Handles cases like "John Smith" matching "jsmith" or "john smith".
func authorNameMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	partsA := strings.Fields(a)
	partsB := strings.Fields(b)
	if len(partsA) >= 2 && len(partsB) == 1 {
		initial := string(partsA[0][0])
		lastName := partsA[len(partsA)-1]
		if partsB[0] == initial+lastName {
			return true
		}
	}
	if len(partsB) >= 2 && len(partsA) == 1 {
		initial := string(partsB[0][0])
		lastName := partsB[len(partsB)-1]
		if partsA[0] == initial+lastName {
			return true
		}
	}
	return false
}

// pqStringArray is a scanner for PostgreSQL text arrays returned by array_agg.
type pqStringArrayScanner struct {
	dest *[]string
}

func pqStringArray(dest *[]string) *pqStringArrayScanner {
	return &pqStringArrayScanner{dest: dest}
}

func (s *pqStringArrayScanner) Scan(src any) error {
	if src == nil {
		*s.dest = nil
		return nil
	}
	switch v := src.(type) {
	case []byte:
		return s.parse(string(v))
	case string:
		return s.parse(v)
	default:
		return fmt.Errorf("unsupported type for string array: %T", src)
	}
}

func (s *pqStringArrayScanner) parse(raw string) error {
	raw = strings.TrimPrefix(raw, "{")
	raw = strings.TrimSuffix(raw, "}")
	if raw == "" {
		*s.dest = nil
		return nil
	}
	*s.dest = strings.Split(raw, ",")
	for i, v := range *s.dest {
		(*s.dest)[i] = strings.Trim(v, `"`)
	}
	return nil
}
