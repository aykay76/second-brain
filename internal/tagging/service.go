package tagging

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"pa/internal/llm"
)

type Config struct {
	BatchSize int
	MaxTags   int
}

type Service struct {
	chat llm.ChatProvider
	db   *sql.DB
	cfg  Config
}

func NewService(chat llm.ChatProvider, db *sql.DB, cfg Config) *Service {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 20
	}
	if cfg.MaxTags <= 0 {
		cfg.MaxTags = 5
	}
	return &Service{chat: chat, db: db, cfg: cfg}
}

type EnrichResult struct {
	Tagged     int `json:"tagged"`
	Summarised int `json:"summarised"`
	Errors     int `json:"errors"`
}

// Enrich processes artifacts that are missing tags or summaries.
func (s *Service) Enrich(ctx context.Context) (*EnrichResult, error) {
	start := time.Now()
	slog.Info("enrichment starting")

	result := &EnrichResult{}

	tagged, tagErrs, err := s.tagUntagged(ctx)
	if err != nil {
		return nil, fmt.Errorf("auto-tagging: %w", err)
	}
	result.Tagged = tagged
	result.Errors += tagErrs

	summarised, sumErrs, err := s.summariseUnsummarised(ctx)
	if err != nil {
		return nil, fmt.Errorf("summarisation: %w", err)
	}
	result.Summarised = summarised
	result.Errors += sumErrs

	slog.Info("enrichment complete",
		"tagged", result.Tagged,
		"summarised", result.Summarised,
		"errors", result.Errors,
		"duration", time.Since(start).Round(time.Millisecond),
	)
	return result, nil
}

type artifactRow struct {
	id    string
	title string
	text  string
}

func (s *Service) tagUntagged(ctx context.Context) (int, int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.title, COALESCE(a.content, a.title) AS text
		FROM artifacts a
		LEFT JOIN tags t ON t.artifact_id = a.id AND t.auto_generated = true
		WHERE t.id IS NULL
		ORDER BY a.ingested_at DESC
		LIMIT $1
	`, s.cfg.BatchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("query untagged: %w", err)
	}
	defer rows.Close()

	var artifacts []artifactRow
	for rows.Next() {
		var a artifactRow
		if err := rows.Scan(&a.id, &a.title, &a.text); err != nil {
			return 0, 0, fmt.Errorf("scan: %w", err)
		}
		artifacts = append(artifacts, a)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	tagged, errors := 0, 0
	for _, a := range artifacts {
		if err := ctx.Err(); err != nil {
			return tagged, errors, err
		}

		tags, err := s.generateTags(ctx, a.title, a.text)
		if err != nil {
			slog.Warn("tag generation failed", "artifact_id", a.id, "error", err)
			errors++
			continue
		}

		if err := s.storeTags(ctx, a.id, tags); err != nil {
			slog.Warn("tag storage failed", "artifact_id", a.id, "error", err)
			errors++
			continue
		}
		tagged++
		slog.Debug("auto-tagged artifact", "artifact_id", a.id, "tags", tags)
	}
	return tagged, errors, nil
}

func (s *Service) summariseUnsummarised(ctx context.Context) (int, int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, COALESCE(content, title) AS text
		FROM artifacts
		WHERE (summary IS NULL OR summary = '') AND content IS NOT NULL AND content != ''
		ORDER BY ingested_at DESC
		LIMIT $1
	`, s.cfg.BatchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("query unsummarised: %w", err)
	}
	defer rows.Close()

	var artifacts []artifactRow
	for rows.Next() {
		var a artifactRow
		if err := rows.Scan(&a.id, &a.title, &a.text); err != nil {
			return 0, 0, fmt.Errorf("scan: %w", err)
		}
		artifacts = append(artifacts, a)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	summarised, errors := 0, 0
	for _, a := range artifacts {
		if err := ctx.Err(); err != nil {
			return summarised, errors, err
		}

		summary, err := s.generateSummary(ctx, a.title, a.text)
		if err != nil {
			slog.Warn("summary generation failed", "artifact_id", a.id, "error", err)
			errors++
			continue
		}

		if err := s.storeSummary(ctx, a.id, summary); err != nil {
			slog.Warn("summary storage failed", "artifact_id", a.id, "error", err)
			errors++
			continue
		}
		summarised++
		slog.Debug("summarised artifact", "artifact_id", a.id)
	}
	return summarised, errors, nil
}

const tagPrompt = `You are a knowledge categorisation assistant. Given the title and content of a knowledge artifact, suggest %d concise, lowercase tags that describe its key topics.

Rules:
- Return ONLY the tags, one per line, no numbering, no bullets, no explanation.
- Tags should be 1-3 words, lowercase, hyphenated if multi-word (e.g. "event-sourcing").
- Focus on the core technical concepts, technologies, and domains.
- Do not include generic tags like "technology" or "programming".

Title: %s

Content:
%s`

func (s *Service) generateTags(ctx context.Context, title, content string) ([]string, error) {
	if len(content) > 2000 {
		content = content[:2000]
	}

	prompt := fmt.Sprintf(tagPrompt, s.cfg.MaxTags, title, content)

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}

	resp, err := s.chat.Complete(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	return parseTags(resp, s.cfg.MaxTags), nil
}

func parseTags(response string, max int) []string {
	var tags []string
	for _, line := range strings.Split(response, "\n") {
		tag := strings.TrimSpace(line)
		tag = strings.TrimLeft(tag, "-•*0123456789.) ")
		tag = strings.ToLower(tag)
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) > 50 {
			continue
		}
		tags = append(tags, tag)
		if len(tags) >= max {
			break
		}
	}
	return tags
}

func (s *Service) storeTags(ctx context.Context, artifactID string, tags []string) error {
	for _, tag := range tags {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO tags (id, artifact_id, tag, auto_generated)
			VALUES (gen_random_uuid(), $1, $2, true)
			ON CONFLICT (artifact_id, tag) DO NOTHING
		`, artifactID, tag)
		if err != nil {
			return fmt.Errorf("insert tag %q: %w", tag, err)
		}
	}
	return nil
}

const summaryPrompt = `Write a concise one-paragraph summary (2-4 sentences) of the following content. Focus on the key points, purpose, and significance. Do not start with "This" or "The article/document". Be direct and informative.

Title: %s

Content:
%s`

func (s *Service) generateSummary(ctx context.Context, title, content string) (string, error) {
	if len(content) > 4000 {
		content = content[:4000]
	}

	prompt := fmt.Sprintf(summaryPrompt, title, content)

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}

	resp, err := s.chat.Complete(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("chat completion: %w", err)
	}

	return strings.TrimSpace(resp), nil
}

func (s *Service) storeSummary(ctx context.Context, artifactID, summary string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE artifacts SET summary = $1, updated_at = NOW() WHERE id = $2
	`, summary, artifactID)
	return err
}
