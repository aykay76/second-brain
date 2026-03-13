package retrieval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pgvector/pgvector-go"

	"pa/internal/llm"
)

type SearchService struct {
	embedder llm.EmbeddingProvider
	db       *sql.DB
}

func NewSearchService(embedder llm.EmbeddingProvider, db *sql.DB) *SearchService {
	return &SearchService{
		embedder: embedder,
		db:       db,
	}
}

type SearchOptions struct {
	Limit          int
	SemanticWeight float64
	FullTextWeight float64
	Tags           []string
}

func DefaultSearchOptions() SearchOptions {
	return SearchOptions{
		Limit:          20,
		SemanticWeight: 0.7,
		FullTextWeight: 0.3,
	}
}

type SearchResult struct {
	ID           string          `json:"id"`
	Source       string          `json:"source"`
	ArtifactType string         `json:"artifact_type"`
	Title        string          `json:"title"`
	Content      *string         `json:"content,omitempty"`
	Summary      *string         `json:"summary,omitempty"`
	SourceURL    *string         `json:"source_url,omitempty"`
	Score        float64         `json:"score"`
	Metadata     json.RawMessage `json:"metadata"`
}

func (s *SearchService) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned for query")
	}

	if len(opts.Tags) > 0 {
		return s.hybridSearchWithTags(ctx, embeddings[0], query, opts)
	}
	return s.hybridSearch(ctx, embeddings[0], query, opts)
}

// SemanticSearch performs vector-only search without full-text.
func (s *SearchService) SemanticSearch(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned for query")
	}

	return s.semanticSearch(ctx, embeddings[0], limit)
}

func (s *SearchService) semanticSearch(ctx context.Context, queryVec []float32, limit int) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			a.id, a.source, a.artifact_type, a.title,
			a.content, a.summary, a.source_url, a.metadata,
			1 - (e.embedding <=> $1::vector) AS score
		FROM artifacts a
		JOIN artifact_embeddings e ON e.artifact_id = a.id
		ORDER BY e.embedding <=> $1::vector
		LIMIT $2
	`, pgvector.NewVector(queryVec), limit)
	if err != nil {
		return nil, fmt.Errorf("semantic search query: %w", err)
	}
	defer rows.Close()

	return scanResults(rows)
}

func (s *SearchService) hybridSearch(ctx context.Context, queryVec []float32, queryText string, opts SearchOptions) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH semantic AS (
			SELECT a.id, 1 - (e.embedding <=> $1::vector) AS score
			FROM artifacts a
			JOIN artifact_embeddings e ON e.artifact_id = a.id
			ORDER BY e.embedding <=> $1::vector
			LIMIT $2
		),
		fulltext AS (
			SELECT id, ts_rank(tsv, websearch_to_tsquery('english', $3)) AS score
			FROM artifacts
			WHERE tsv @@ websearch_to_tsquery('english', $3)
			ORDER BY score DESC
			LIMIT $2
		)
		SELECT
			a.id, a.source, a.artifact_type, a.title,
			a.content, a.summary, a.source_url, a.metadata,
			COALESCE(s.score, 0) * $4 + COALESCE(f.score, 0) * $5 AS combined_score
		FROM (
			SELECT id FROM semantic
			UNION
			SELECT id FROM fulltext
		) ids
		JOIN artifacts a ON a.id = ids.id
		LEFT JOIN semantic s ON s.id = a.id
		LEFT JOIN fulltext f ON f.id = a.id
		ORDER BY combined_score DESC
		LIMIT $2
	`, pgvector.NewVector(queryVec), opts.Limit, queryText, opts.SemanticWeight, opts.FullTextWeight)
	if err != nil {
		return nil, fmt.Errorf("hybrid search query: %w", err)
	}
	defer rows.Close()

	return scanResults(rows)
}

func (s *SearchService) hybridSearchWithTags(ctx context.Context, queryVec []float32, queryText string, opts SearchOptions) ([]SearchResult, error) {
	tagList := "{" + strings.Join(opts.Tags, ",") + "}"

	rows, err := s.db.QueryContext(ctx, `
		WITH tag_filtered AS (
			SELECT DISTINCT artifact_id
			FROM tags
			WHERE tag = ANY($6::text[])
			GROUP BY artifact_id
			HAVING count(DISTINCT tag) = $7
		),
		semantic AS (
			SELECT a.id, 1 - (e.embedding <=> $1::vector) AS score
			FROM artifacts a
			JOIN artifact_embeddings e ON e.artifact_id = a.id
			JOIN tag_filtered tf ON tf.artifact_id = a.id
			ORDER BY e.embedding <=> $1::vector
			LIMIT $2
		),
		fulltext AS (
			SELECT a.id, ts_rank(a.tsv, websearch_to_tsquery('english', $3)) AS score
			FROM artifacts a
			JOIN tag_filtered tf ON tf.artifact_id = a.id
			WHERE a.tsv @@ websearch_to_tsquery('english', $3)
			ORDER BY score DESC
			LIMIT $2
		)
		SELECT
			a.id, a.source, a.artifact_type, a.title,
			a.content, a.summary, a.source_url, a.metadata,
			COALESCE(s.score, 0) * $4 + COALESCE(f.score, 0) * $5 AS combined_score
		FROM (
			SELECT id FROM semantic
			UNION
			SELECT id FROM fulltext
		) ids
		JOIN artifacts a ON a.id = ids.id
		LEFT JOIN semantic s ON s.id = a.id
		LEFT JOIN fulltext f ON f.id = a.id
		ORDER BY combined_score DESC
		LIMIT $2
	`, pgvector.NewVector(queryVec), opts.Limit, queryText, opts.SemanticWeight, opts.FullTextWeight, tagList, len(opts.Tags))
	if err != nil {
		return nil, fmt.Errorf("hybrid search with tags: %w", err)
	}
	defer rows.Close()

	return scanResults(rows)
}

func scanResults(rows *sql.Rows) ([]SearchResult, error) {
	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(
			&r.ID, &r.Source, &r.ArtifactType, &r.Title,
			&r.Content, &r.Summary, &r.SourceURL, &r.Metadata,
			&r.Score,
		); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		results = append(results, r)
	}
	if results == nil {
		results = []SearchResult{}
	}
	return results, rows.Err()
}
