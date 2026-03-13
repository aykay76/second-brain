package onedrive

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

const maxEmbeddingContentLen = 4000

// Syncer implements ingestion.Syncer for Microsoft OneDrive.
type Syncer struct {
	db       *sql.DB
	embedSvc *retrieval.EmbeddingService
	client   *Client
	cfg      config.OneDriveConfig
}

func NewSyncer(db *sql.DB, embedSvc *retrieval.EmbeddingService, cfg config.OneDriveConfig) *Syncer {
	return &Syncer{
		db:       db,
		embedSvc: embedSvc,
		client:   NewClient(cfg),
		cfg:      cfg,
	}
}

func (s *Syncer) Name() string { return "onedrive" }

func (s *Syncer) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	if err := s.client.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	slog.Info("onedrive sync starting", "folders", len(s.cfg.Folders))

	extensions := s.cfg.Extensions
	if len(extensions) == 0 {
		extensions = []string{".md", ".txt", ".docx", ".pdf"}
	}

	for _, folder := range s.cfg.Folders {
		if err := s.syncFolder(ctx, folder, extensions, result); err != nil {
			slog.Error("failed to sync onedrive folder", "folder", folder, "error", err)
			result.Errors++
		}
	}

	slog.Info("onedrive sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

func (s *Syncer) syncFolder(ctx context.Context, folder string, extensions []string, result *ingestion.SyncResult) error {
	cursorKey := "onedrive:delta:" + folder
	deltaLink := s.getCursor(ctx, cursorKey)

	var deltaLinkStr string
	if deltaLink != "" {
		deltaLinkStr = deltaLink
	}

	resp, err := s.client.DeltaQuery(ctx, folder, deltaLinkStr)
	if err != nil {
		slog.Warn("delta query failed, falling back to full listing", "folder", folder, "error", err)
		return s.syncFolderFull(ctx, folder, extensions, result)
	}

	for _, item := range resp.Items {
		if item.IsFolder {
			continue
		}

		if item.IsDeleted {
			s.handleDeletion(ctx, item)
			continue
		}

		if !IsSupportedExtension(item.Name, extensions) {
			continue
		}

		if err := s.syncItem(ctx, item, folder, result); err != nil {
			slog.Error("failed to sync onedrive item",
				"name", item.Name,
				"id", item.ID,
				"error", err,
			)
			result.Errors++
		}
	}

	if resp.DeltaLink != "" {
		s.setCursorString(ctx, cursorKey, resp.DeltaLink)
	}

	return nil
}

func (s *Syncer) syncFolderFull(ctx context.Context, folder string, extensions []string, result *ingestion.SyncResult) error {
	items, err := s.client.ListFolder(ctx, folder)
	if err != nil {
		return err
	}

	for _, item := range items {
		if item.IsFolder {
			continue
		}

		if !IsSupportedExtension(item.Name, extensions) {
			continue
		}

		if err := s.syncItem(ctx, item, folder, result); err != nil {
			slog.Error("failed to sync onedrive item",
				"name", item.Name,
				"id", item.ID,
				"error", err,
			)
			result.Errors++
		}
	}

	return nil
}

func (s *Syncer) syncItem(ctx context.Context, item DriveItem, folder string, result *ingestion.SyncResult) error {
	var data []byte
	var err error

	if item.DownloadURL != "" {
		data, err = s.client.DownloadContent(ctx, item.DownloadURL)
	} else {
		data, err = s.client.DownloadItem(ctx, item.ID)
	}
	if err != nil {
		return fmt.Errorf("download %q: %w", item.Name, err)
	}

	text, err := ExtractText(item.Name, data)
	if err != nil {
		return fmt.Errorf("extract text from %q: %w", item.Name, err)
	}

	if strings.TrimSpace(text) == "" {
		result.Skipped++
		return nil
	}

	hash := sha256Hash(text)
	externalID := "onedrive:" + item.ID

	if s.isUnchanged(ctx, externalID, hash) {
		result.Skipped++
		return nil
	}

	metadata := map[string]any{
		"folder":        folder,
		"file_name":     item.Name,
		"size":          item.Size,
		"mime_type":     item.MimeType,
		"web_url":       item.WebURL,
		"parent_path":   item.ParentPath,
		"last_modified": item.LastModified.Format(time.RFC3339),
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	createdAt := item.LastModified
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	var id string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata, content_hash, source_url, created_at)
		VALUES ('onedrive', 'document', $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			content_hash = EXCLUDED.content_hash,
			updated_at = NOW()
		RETURNING id`,
		externalID, item.Name, text, metadataJSON, hash, item.WebURL, createdAt,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("upsert artifact: %w", err)
	}

	embeddingText := truncateForEmbedding(item.Name + "\n" + text)
	if err := s.embedSvc.EmbedArtifact(ctx, id, embeddingText); err != nil {
		slog.Warn("failed to generate embedding", "name", item.Name, "error", err)
	}

	result.Ingested++
	slog.Info("ingested onedrive document", "name", item.Name, "id", id)
	return nil
}

func (s *Syncer) handleDeletion(ctx context.Context, item DriveItem) {
	externalID := "onedrive:" + item.ID
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM artifacts WHERE source = 'onedrive' AND external_id = $1`,
		externalID,
	)
	if err != nil {
		slog.Warn("failed to delete onedrive artifact", "id", item.ID, "error", err)
		return
	}
	slog.Info("deleted onedrive artifact", "item_id", item.ID)
}

// --- DB helpers ---

func (s *Syncer) isUnchanged(ctx context.Context, externalID, hash string) bool {
	var existingHash sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM artifacts WHERE source = 'onedrive' AND external_id = $1`,
		externalID,
	).Scan(&existingHash)
	return err == nil && existingHash.Valid && existingHash.String == hash
}

func (s *Syncer) getCursor(ctx context.Context, name string) string {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor_value FROM sync_cursors WHERE source_name = $1`, name,
	).Scan(&val)
	if err != nil {
		return ""
	}
	return val
}

func (s *Syncer) setCursorString(ctx context.Context, name, value string) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_cursors (source_name, cursor_value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (source_name) DO UPDATE SET
			cursor_value = EXCLUDED.cursor_value,
			updated_at = NOW()`,
		name, value,
	)
	if err != nil {
		slog.Warn("failed to update sync cursor", "name", name, "error", err)
	}
}

func truncateForEmbedding(text string) string {
	if len(text) > maxEmbeddingContentLen {
		return text[:maxEmbeddingContentLen]
	}
	return text
}

func sha256Hash(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}
