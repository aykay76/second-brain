package retrieval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"pa/internal/llm"
)

const (
	defaultTopK           = 10
	defaultMaxRelated     = 5
	defaultMaxContentLen  = 1500
	defaultMaxContextLen  = 12000
)

var citationRe = regexp.MustCompile(`\[(\d+)]`)

type RAGService struct {
	search *SearchService
	chat   llm.ChatProvider
	db     *sql.DB
}

func NewRAGService(search *SearchService, chat llm.ChatProvider, db *sql.DB) *RAGService {
	return &RAGService{
		search: search,
		chat:   chat,
		db:     db,
	}
}

type AskRequest struct {
	Question string
	TopK     int
}

type Source struct {
	Index        int     `json:"index"`
	ArtifactID   string  `json:"artifact_id"`
	Title        string  `json:"title"`
	SourceType   string  `json:"source"`
	ArtifactType string  `json:"artifact_type"`
	SourceURL    *string `json:"source_url,omitempty"`
	Score        float64 `json:"score,omitempty"`
	Cited        bool    `json:"cited"`
}

type AskResponse struct {
	Question string   `json:"question"`
	Answer   string   `json:"answer"`
	Sources  []Source `json:"sources"`
}

func (s *RAGService) Ask(ctx context.Context, req AskRequest) (*AskResponse, error) {
	topK := req.TopK
	if topK <= 0 {
		topK = defaultTopK
	}

	results, err := s.search.Search(ctx, req.Question, SearchOptions{
		Limit:          topK,
		SemanticWeight: 0.7,
		FullTextWeight: 0.3,
	})
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		return &AskResponse{
			Question: req.Question,
			Answer:   "I couldn't find any relevant information in your knowledge base to answer this question.",
			Sources:  []Source{},
		}, nil
	}

	related, err := s.enrichWithRelated(ctx, results)
	if err != nil {
		slog.Warn("failed to enrich with related artifacts", "error", err)
	}

	allResults := deduplicateResults(results, related)

	sources, contextBlock := assembleContext(allResults)

	answer, err := s.complete(ctx, req.Question, contextBlock)
	if err != nil {
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	markCitedSources(answer, sources)

	return &AskResponse{
		Question: req.Question,
		Answer:   answer,
		Sources:  sources,
	}, nil
}

func (s *RAGService) enrichWithRelated(ctx context.Context, results []SearchResult) ([]SearchResult, error) {
	if len(results) == 0 {
		return nil, nil
	}

	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT a.id, a.source, a.artifact_type, a.title,
			a.content, a.summary, a.source_url, a.metadata
		FROM relationships r
		JOIN artifacts a ON (
			(a.id = r.target_id AND r.source_id = ANY($1::uuid[]))
			OR
			(a.id = r.source_id AND r.target_id = ANY($1::uuid[]))
		)
		WHERE a.id != ALL($1::uuid[])
		LIMIT $2
	`, "{"+strings.Join(ids, ",")+"}", defaultMaxRelated)
	if err != nil {
		return nil, fmt.Errorf("query related: %w", err)
	}
	defer rows.Close()

	var related []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(
			&r.ID, &r.Source, &r.ArtifactType, &r.Title,
			&r.Content, &r.Summary, &r.SourceURL, &r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("scan related: %w", err)
		}
		related = append(related, r)
	}
	return related, rows.Err()
}

func deduplicateResults(primary, related []SearchResult) []SearchResult {
	seen := make(map[string]bool, len(primary))
	all := make([]SearchResult, 0, len(primary)+len(related))
	for _, r := range primary {
		seen[r.ID] = true
		all = append(all, r)
	}
	for _, r := range related {
		if !seen[r.ID] {
			seen[r.ID] = true
			all = append(all, r)
		}
	}
	return all
}

func assembleContext(results []SearchResult) ([]Source, string) {
	var b strings.Builder
	sources := make([]Source, 0, len(results))
	totalLen := 0

	for i, r := range results {
		if totalLen >= defaultMaxContextLen {
			break
		}

		idx := i + 1

		content := pickContent(r)
		if len(content) > defaultMaxContentLen {
			content = content[:defaultMaxContentLen] + "..."
		}

		sourceLabel := fmt.Sprintf("[%d] (source: %s, type: %s", idx, r.Source, r.ArtifactType)
		if r.SourceURL != nil && *r.SourceURL != "" {
			sourceLabel += ", url: " + *r.SourceURL
		}
		sourceLabel += ")"

		entry := fmt.Sprintf("%s\nTitle: %s\n%s\n\n", sourceLabel, r.Title, content)
		b.WriteString(entry)
		totalLen += len(entry)

		sources = append(sources, Source{
			Index:        idx,
			ArtifactID:   r.ID,
			Title:        r.Title,
			SourceType:   r.Source,
			ArtifactType: r.ArtifactType,
			SourceURL:    r.SourceURL,
			Score:        r.Score,
		})
	}

	return sources, b.String()
}

func pickContent(r SearchResult) string {
	if r.Summary != nil && *r.Summary != "" {
		return *r.Summary
	}
	if r.Content != nil && *r.Content != "" {
		return *r.Content
	}
	return "(no content available)"
}

const systemPrompt = `You are a personal knowledge assistant. Answer the user's question based ONLY on the provided context. Do not use any outside knowledge.

Rules:
- Cite your sources using [N] notation (e.g. [1], [2]) corresponding to the numbered sources in the context.
- If multiple sources support a point, cite all of them (e.g. [1][3]).
- If the context does not contain enough information to answer, say "I don't have enough information in my knowledge base to answer this."
- Be concise and direct.
- Preserve technical accuracy from the source material.`

func (s *RAGService) complete(ctx context.Context, question, contextBlock string) (string, error) {
	userPrompt := fmt.Sprintf("Context:\n%s\nQuestion: %s", contextBlock, question)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}

	return s.chat.Complete(ctx, messages)
}

func markCitedSources(answer string, sources []Source) {
	cited := make(map[int]bool)
	for _, match := range citationRe.FindAllStringSubmatch(answer, -1) {
		if n, err := strconv.Atoi(match[1]); err == nil {
			cited[n] = true
		}
	}
	for i := range sources {
		if cited[sources[i].Index] {
			sources[i].Cited = true
		}
	}
}

// ContextForTesting exposes context assembly for test verification.
func ContextForTesting(results []SearchResult) ([]Source, string) {
	return assembleContext(results)
}

// ExtractCitedIndices returns the set of citation indices found in text.
func ExtractCitedIndices(text string) map[int]bool {
	cited := make(map[int]bool)
	for _, match := range citationRe.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(match[1]); err == nil {
			cited[n] = true
		}
	}
	return cited
}

// MarshalMetadata is a helper to convert a value to json.RawMessage for tests.
func MarshalMetadata(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
