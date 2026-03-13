package filesystem

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

var wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// ParseWikilinks extracts Obsidian-style wikilink targets from content.
// Handles [[page]], [[page|alias]], and [[page#heading]] syntax.
func ParseWikilinks(content string) []string {
	matches := wikilinkRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var targets []string

	for _, m := range matches {
		target := m[1]

		// [[page|alias]] — take page part
		if i := strings.Index(target, "|"); i >= 0 {
			target = target[:i]
		}
		// [[page#heading]] — take page part
		if i := strings.Index(target, "#"); i >= 0 {
			target = target[:i]
		}

		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		targets = append(targets, target)
	}
	return targets
}

// processWikilinks parses wikilinks from content and creates LINKS_TO
// relationships to matching filesystem artifacts. Targets that haven't
// been ingested yet are silently skipped.
func (s *Scanner) processWikilinks(ctx context.Context, sourceArtifactID, content string) error {
	targets := ParseWikilinks(content)
	if len(targets) == 0 {
		return nil
	}

	for _, target := range targets {
		targetID, err := s.resolveWikilinkTarget(ctx, target)
		if err != nil {
			slog.Debug("wikilink target not found", "target", target, "error", err)
			continue
		}
		if targetID == sourceArtifactID {
			continue
		}

		_, err = s.db.ExecContext(ctx, `
			INSERT INTO relationships (source_id, target_id, relation_type, confidence, metadata)
			VALUES ($1, $2, 'LINKS_TO', 1.0, '{}')
			ON CONFLICT (source_id, target_id, relation_type) DO NOTHING`,
			sourceArtifactID, targetID,
		)
		if err != nil {
			return fmt.Errorf("create wikilink relationship for %q: %w", target, err)
		}
	}
	return nil
}

// resolveWikilinkTarget finds an artifact matching the wikilink target name.
// Matches against filename (without extension) using the artifact title or
// external_id path suffix.
func (s *Scanner) resolveWikilinkTarget(ctx context.Context, target string) (string, error) {
	var id string

	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM artifacts
		WHERE source = 'filesystem'
		AND (
			title = $1
			OR external_id LIKE '%/' || $1 || '.md'
			OR external_id LIKE '%/' || $1 || '.txt'
		)
		LIMIT 1`,
		target,
	).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("target %q not found", target)
		}
		return "", err
	}
	return id, nil
}
