package vision

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

const (
	sourcePrefix = "image:"
	artifactType = "photo"
)

var defaultCaptioningPrompt = "Provide a concise, informative caption for this image that would help someone understand what's in the photo. Focus on the main subjects, objects, activities, and setting. Be specific and descriptive."

// FilesystemSyncer scans for images in the filesystem and generates captions.
type FilesystemSyncer struct {
	db               *sql.DB
	embedSvc         *retrieval.EmbeddingService
	visionSvc        *VisionService
	paths            []string
	extensions       map[string]bool
	captioningPrompt string
	// For testing: if skipCaption is true, don't actually call vision model
	skipCaption bool
}

// NewFilesystemSyncer creates a new image filesystem syncer.
func NewFilesystemSyncer(
	db *sql.DB,
	embedSvc *retrieval.EmbeddingService,
	visionSvc *VisionService,
	cfg config.VisionConfig,
) *FilesystemSyncer {
	exts := make(map[string]bool, len(cfg.Extensions))
	for _, ext := range cfg.Extensions {
		exts[strings.ToLower(ext)] = true
	}

	prompt := cfg.CaptioningPrompt
	if prompt == "" {
		prompt = defaultCaptioningPrompt
	}

	return &FilesystemSyncer{
		db:               db,
		embedSvc:         embedSvc,
		visionSvc:        visionSvc,
		paths:            cfg.Paths,
		extensions:       exts,
		captioningPrompt: prompt,
	}
}

func (s *FilesystemSyncer) Name() string { return "vision" }

func (s *FilesystemSyncer) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	for _, root := range s.paths {
		if err := s.scanDirectory(ctx, expandHome(root), result); err != nil {
			return result, err
		}
	}

	slog.Info("vision sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

func (s *FilesystemSyncer) scanDirectory(ctx context.Context, dir string, result *ingestion.SyncResult) error {
	slog.Info("scanning directory for images", "path", dir)

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

func (s *FilesystemSyncer) processAndCount(ctx context.Context, path string, result *ingestion.SyncResult) error {
	ingested, err := s.processImage(ctx, path)
	if err != nil {
		slog.Error("failed to process image", "path", path, "error", err)
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

func (s *FilesystemSyncer) processImage(ctx context.Context, imagePath string) (bool, error) {
	// Extract metadata
	metadata, err := s.visionSvc.ExtractMetadata(imagePath)
	if err != nil {
		return false, fmt.Errorf("extract metadata: %w", err)
	}

	// Create external ID from file path (using relative path and hash for uniqueness)
	externalID := generateImageID(imagePath)

	// Check if already ingested
	var existingID string
	err = s.db.QueryRowContext(ctx,
		"SELECT id FROM artifacts WHERE source = $1 AND external_id = $2",
		sourcePrefix+s.Name(), externalID,
	).Scan(&existingID)

	if err == nil {
		// Already exists
		slog.Debug("image already ingested", "path", imagePath, "external_id", externalID)
		return false, nil
	}
	if err != sql.ErrNoRows {
		return false, fmt.Errorf("check existing: %w", err)
	}

	// Generate caption
	var caption string
	if !s.skipCaption {
		var captionErr error
		caption, captionErr = s.visionSvc.Caption(ctx, imagePath, s.captioningPrompt)
		if captionErr != nil {
			return false, fmt.Errorf("generate caption: %w", captionErr)
		}
	} else {
		caption = fmt.Sprintf("[Caption not generated] Image at %s (%dx%d)", imagePath, metadata.Width, metadata.Height)
	}

	// Store metadata as JSON
	metadataJSON, _ := json.Marshal(metadata)

	// Create artifact
	title := metadata.FileName
	now := time.Now().UTC()

	var artifactID string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (
			source, artifact_type, external_id, title, content, 
			metadata, source_url, created_at, ingested_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`,
		sourcePrefix+s.Name(), artifactType, externalID, title, caption,
		string(metadataJSON), imagePath, now, now, now,
	).Scan(&artifactID)

	if err != nil {
		return false, fmt.Errorf("insert artifact: %w", err)
	}

	// Embed the caption
	if err := s.embedSvc.EmbedArtifact(ctx, artifactID, caption); err != nil {
		slog.Warn("failed to generate embedding", "path", imagePath, "error", err)
	}

	slog.Debug("image ingested", "path", imagePath, "title", title)
	return true, nil
}

func generateImageID(imagePath string) string {
	// Create a hash of the full path for uniqueness
	h := sha256.New()
	h.Write([]byte(imagePath))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
