package filesystem

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pa/internal/config"
	"pa/internal/ingestion"
	"pa/internal/retrieval"
)

type FileMetadata struct {
	FilePath    string         `json:"file_path"`
	FileName    string         `json:"file_name"`
	Directory   string         `json:"directory"`
	Extension   string         `json:"extension"`
	Size        int64          `json:"size"`
	ModTime     time.Time      `json:"mod_time"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
}

type Scanner struct {
	db         *sql.DB
	embedSvc   *retrieval.EmbeddingService
	paths      []string
	extensions map[string]bool
}

func NewScanner(db *sql.DB, embedSvc *retrieval.EmbeddingService, cfg config.FilesystemConfig) *Scanner {
	exts := make(map[string]bool, len(cfg.Extensions))
	for _, ext := range cfg.Extensions {
		exts[strings.ToLower(ext)] = true
	}
	return &Scanner{
		db:         db,
		embedSvc:   embedSvc,
		paths:      cfg.Paths,
		extensions: exts,
	}
}

func (s *Scanner) Name() string { return "filesystem" }

func (s *Scanner) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	for _, root := range s.paths {
		if err := s.scanDirectory(ctx, expandHome(root), result); err != nil {
			return result, err
		}
	}

	slog.Info("filesystem sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

func (s *Scanner) scanDirectory(ctx context.Context, dir string, result *ingestion.SyncResult) error {
	slog.Info("scanning directory", "path", dir)

	info, err := os.Stat(dir)
	if err != nil {
		slog.Warn("cannot access directory", "path", dir, "error", err)
		return nil
	}
	if !info.IsDir() {
		slog.Warn("path is not a directory", "path", dir)
		return nil
	}

	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("walk error", "path", path, "error", err)
			return nil
		}
		if d.IsDir() || !s.extensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.processAndCount(ctx, path, result)
	})
}

func (s *Scanner) processAndCount(ctx context.Context, path string, result *ingestion.SyncResult) error {
	ingested, err := s.ProcessFile(ctx, path)
	if err != nil {
		slog.Error("failed to process file", "path", path, "error", err)
		result.Errors++
		return nil
	}
	if ingested {
		result.Ingested++
	} else {
		result.Skipped++
	}
	return nil
}

// ProcessFile ingests a single file. Returns true if the file was ingested
// (new or changed), false if skipped (unchanged).
func (s *Scanner) ProcessFile(ctx context.Context, path string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read file: %w", err)
	}

	hash := sha256Hash(content)
	if s.isUnchanged(ctx, path, hash) {
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat file: %w", err)
	}

	title, textContent, meta := s.extractContent(path, string(content), info)

	metadataJSON, err := json.Marshal(meta)
	if err != nil {
		return false, fmt.Errorf("marshal metadata: %w", err)
	}

	artifactID, err := s.upsertArtifact(ctx, path, title, textContent, metadataJSON, hash, info.ModTime())
	if err != nil {
		return false, err
	}

	if isMarkdown(path) {
		if err := s.processWikilinks(ctx, artifactID, string(content)); err != nil {
			slog.Warn("failed to process wikilinks", "path", path, "error", err)
		}
	}

	embeddingText := title + "\n" + textContent
	// Truncate to avoid exceeding embedding model context length
	const maxEmbeddingContentLen = 4000
	if len(embeddingText) > maxEmbeddingContentLen {
		embeddingText = embeddingText[:maxEmbeddingContentLen]
	}
	if err := s.embedSvc.EmbedArtifact(ctx, artifactID, embeddingText); err != nil {
		slog.Warn("failed to generate embedding", "path", path, "error", err)
	}

	slog.Info("ingested file", "path", path, "id", artifactID)
	return true, nil
}

func (s *Scanner) isUnchanged(ctx context.Context, path, hash string) bool {
	var existingHash sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM artifacts WHERE source = 'filesystem' AND external_id = $1`,
		path,
	).Scan(&existingHash)
	return err == nil && existingHash.Valid && existingHash.String == hash
}

func (s *Scanner) extractContent(path, rawContent string, info os.FileInfo) (title, body string, meta FileMetadata) {
	title = filenameWithoutExt(path)
	body = rawContent

	meta = FileMetadata{
		FilePath:  path,
		FileName:  filepath.Base(path),
		Directory: filepath.Dir(path),
		Extension: filepath.Ext(path),
		Size:      info.Size(),
		ModTime:   info.ModTime(),
	}

	if isMarkdown(path) {
		fm, parsedBody := ParseFrontmatter(rawContent)
		if fm != nil {
			meta.Frontmatter = fm
			if t, ok := fm["title"].(string); ok && t != "" {
				title = t
			}
		}
		body = parsedBody
	}
	return title, body, meta
}

func (s *Scanner) upsertArtifact(ctx context.Context, path, title, content string, metadata []byte, hash string, modTime time.Time) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata, content_hash, source_url, created_at)
		VALUES ('filesystem', $1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			content_hash = EXCLUDED.content_hash,
			updated_at = NOW()
		RETURNING id`,
		inferArtifactType(path), path, title, content, metadata, hash, "file://"+path, modTime,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert artifact: %w", err)
	}
	return id, nil
}

func isMarkdown(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".md"
}

func sha256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

func filenameWithoutExt(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func inferArtifactType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md":
		return "note"
	case ".txt":
		return "document"
	case ".pdf":
		return "document"
	default:
		return "document"
	}
}
