package youtube

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

	"github.com/pgvector/pgvector-go"

	"pa/internal/config"
	"pa/internal/ingestion"
	"pa/internal/retrieval"
)

const (
	maxEmbeddingContentLen = 4000

	similarityThreshold       = 0.80
	maxRelationshipCandidates = 5
)

type Syncer struct {
	db          *sql.DB
	embedSvc    *retrieval.EmbeddingService
	client      *Client
	transcript  *TranscriptFetcher
	cfg         config.YouTubeConfig
}

func NewSyncer(db *sql.DB, embedSvc *retrieval.EmbeddingService, cfg config.YouTubeConfig) *Syncer {
	return &Syncer{
		db:         db,
		embedSvc:   embedSvc,
		client:     NewClient(cfg.APIKey),
		transcript: NewTranscriptFetcher(),
		cfg:        cfg,
	}
}

func (s *Syncer) Name() string { return "youtube" }

func (s *Syncer) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	slog.Info("youtube sync starting",
		"channels", len(s.cfg.Channels),
		"search_terms", len(s.cfg.SearchTerms),
	)

	maxResults := s.cfg.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

	allVideos := s.fetchAllVideos(ctx, maxResults, result)
	allVideos = dedup(allVideos)

	slog.Info("youtube videos found", "count", len(allVideos))

	if err := s.client.EnrichVideos(ctx, allVideos); err != nil {
		slog.Warn("failed to enrich video details", "error", err)
	}

	syncedIDs, latestByChannel := s.syncAllVideos(ctx, allVideos, result)

	for _, id := range syncedIDs {
		s.detectRelatedContent(ctx, id)
	}

	s.updateCursors(ctx, latestByChannel)

	slog.Info("youtube sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

func (s *Syncer) fetchAllVideos(ctx context.Context, maxResults int, result *ingestion.SyncResult) []Video {
	var allVideos []Video

	for _, channelID := range s.cfg.Channels {
		cursor := s.getCursor(ctx, "youtube:channel:"+channelID)
		videos, err := s.client.SearchByChannel(ctx, channelID, cursor, maxResults)
		if err != nil {
			slog.Error("failed to search youtube channel", "channel", channelID, "error", err)
			result.Errors++
			continue
		}
		allVideos = append(allVideos, videos...)
	}

	for _, term := range s.cfg.SearchTerms {
		cursor := s.getCursor(ctx, "youtube:search:"+term)
		videos, err := s.client.SearchByQuery(ctx, term, cursor, maxResults)
		if err != nil {
			slog.Error("failed to search youtube", "term", term, "error", err)
			result.Errors++
			continue
		}
		allVideos = append(allVideos, videos...)
	}

	return allVideos
}

func (s *Syncer) syncAllVideos(ctx context.Context, videos []Video, result *ingestion.SyncResult) ([]string, map[string]time.Time) {
	var syncedIDs []string
	latestByChannel := make(map[string]time.Time)

	for i := range videos {
		id, err := s.syncVideo(ctx, &videos[i], result)
		if err != nil {
			slog.Error("failed to sync video", "video_id", videos[i].VideoID, "error", err)
			result.Errors++
			continue
		}
		if id != "" {
			syncedIDs = append(syncedIDs, id)
		}

		if t := videos[i].PublishedAt; !t.IsZero() {
			if cur, ok := latestByChannel[videos[i].ChannelID]; !ok || t.After(cur) {
				latestByChannel[videos[i].ChannelID] = t
			}
		}
	}

	return syncedIDs, latestByChannel
}

func (s *Syncer) updateCursors(ctx context.Context, latestByChannel map[string]time.Time) {
	for _, channelID := range s.cfg.Channels {
		if t, ok := latestByChannel[channelID]; ok {
			s.setCursor(ctx, "youtube:channel:"+channelID, t)
		}
	}
}

func (s *Syncer) syncVideo(ctx context.Context, video *Video, result *ingestion.SyncResult) (string, error) {
	content := video.Description

	transcript, err := s.transcript.Fetch(ctx, video.VideoID)
	if err != nil {
		slog.Warn("failed to fetch transcript", "video_id", video.VideoID, "error", err)
	}
	if transcript != "" {
		content += "\n\n[Transcript]\n" + transcript
	}

	hash := sha256Hash(content)
	externalID := "youtube:" + video.VideoID

	if s.isUnchanged(ctx, externalID, hash) {
		result.Skipped++
		return "", nil
	}

	hasTranscript := transcript != ""
	metadata := map[string]any{
		"channel":        video.Channel,
		"channel_id":     video.ChannelID,
		"duration":       video.Duration,
		"view_count":     video.ViewCount,
		"like_count":     video.LikeCount,
		"tags":           video.Tags,
		"has_transcript":  hasTranscript,
		"thumbnail":      video.Thumbnail,
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}

	sourceURL := "https://www.youtube.com/watch?v=" + video.VideoID
	createdAt := video.PublishedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	var id string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata, content_hash, source_url, created_at)
		VALUES ('youtube', 'video', $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			content_hash = EXCLUDED.content_hash,
			updated_at = NOW()
		RETURNING id`,
		externalID, video.Title, content, metadataJSON, hash, sourceURL, createdAt,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert artifact: %w", err)
	}

	embeddingText := buildEmbeddingText(video.Title, video.Description, transcript)
	if err := s.embedSvc.EmbedArtifact(ctx, id, embeddingText); err != nil {
		slog.Warn("failed to generate embedding", "video_id", video.VideoID, "error", err)
	}

	result.Ingested++
	return id, nil
}

// buildEmbeddingText constructs text for embedding from title, description,
// and an optional transcript. Long transcripts are truncated to keep the
// embedding input manageable.
func buildEmbeddingText(title, description, transcript string) string {
	var b strings.Builder
	b.WriteString(title)
	b.WriteByte('\n')
	b.WriteString(description)

	if transcript != "" {
		b.WriteString("\n\n")
		remaining := maxEmbeddingContentLen - b.Len()
		if remaining > 0 {
			if len(transcript) > remaining {
				b.WriteString(transcript[:remaining])
			} else {
				b.WriteString(transcript)
			}
		}
	}

	return b.String()
}

// detectRelatedContent links a video to papers and repos when embedding
// similarity is high enough.
func (s *Syncer) detectRelatedContent(ctx context.Context, artifactID string) {
	var embedding pgvector.Vector
	err := s.db.QueryRowContext(ctx,
		`SELECT embedding FROM artifact_embeddings WHERE artifact_id = $1 LIMIT 1`,
		artifactID,
	).Scan(&embedding)
	if err != nil {
		return
	}

	targets := []struct {
		source       string
		artifactType string
	}{
		{"arxiv", "paper"},
		{"github", "repo"},
		{"github_trending", "trending_repo"},
	}

	for _, t := range targets {
		rows, err := s.db.QueryContext(ctx, `
			SELECT a.id, a.title, 1 - (e.embedding <=> $1::vector) AS similarity
			FROM artifacts a
			JOIN artifact_embeddings e ON e.artifact_id = a.id
			WHERE a.source = $2 AND a.artifact_type = $3 AND a.id != $4
			ORDER BY e.embedding <=> $1::vector
			LIMIT $5`,
			&embedding, t.source, t.artifactType, artifactID, maxRelationshipCandidates,
		)
		if err != nil {
			slog.Warn("failed to search for related artifacts",
				"artifact_id", artifactID, "target_source", t.source, "error", err)
			continue
		}

		for rows.Next() {
			var targetID, targetTitle string
			var similarity float64
			if err := rows.Scan(&targetID, &targetTitle, &similarity); err != nil {
				continue
			}
			if similarity < similarityThreshold {
				continue
			}

			meta := fmt.Sprintf(`{"similarity": %.4f, "method": "embedding"}`, similarity)
			s.createRelationship(ctx, artifactID, targetID, "RELATED_TO", similarity, meta)

			slog.Info("video relationship detected",
				"type", "RELATED_TO",
				"target_source", t.source,
				"target_title", targetTitle,
				"similarity", fmt.Sprintf("%.4f", similarity),
			)
		}
		rows.Close()
	}
}

func (s *Syncer) createRelationship(ctx context.Context, sourceID, targetID, relationType string, confidence float64, metadata string) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO relationships (source_id, target_id, relation_type, confidence, metadata)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (source_id, target_id, relation_type) DO UPDATE SET
			confidence = EXCLUDED.confidence,
			metadata = EXCLUDED.metadata`,
		sourceID, targetID, relationType, confidence, metadata,
	)
	if err != nil {
		slog.Warn("failed to create relationship",
			"source", sourceID, "target", targetID, "type", relationType, "error", err)
	}
}

// --- DB helpers ---

func (s *Syncer) isUnchanged(ctx context.Context, externalID, hash string) bool {
	var existingHash sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM artifacts WHERE source = 'youtube' AND external_id = $1`,
		externalID,
	).Scan(&existingHash)
	return err == nil && existingHash.Valid && existingHash.String == hash
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

func dedup(videos []Video) []Video {
	seen := make(map[string]bool, len(videos))
	out := make([]Video, 0, len(videos))
	for _, v := range videos {
		if seen[v.VideoID] {
			continue
		}
		seen[v.VideoID] = true
		out = append(out, v)
	}
	return out
}

func sha256Hash(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}
