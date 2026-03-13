package retrieval

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/pgvector/pgvector-go"

	"pa/internal/llm"
)

const defaultBatchSize = 10

type EmbeddingService struct {
	provider  llm.EmbeddingProvider
	db        *sql.DB
	batchSize int
}

func NewEmbeddingService(provider llm.EmbeddingProvider, db *sql.DB) *EmbeddingService {
	return &EmbeddingService{
		provider:  provider,
		db:        db,
		batchSize: defaultBatchSize,
	}
}

type ArtifactText struct {
	ArtifactID string
	Text       string
}

func (s *EmbeddingService) EmbedArtifact(ctx context.Context, artifactID, text string) error {
	embeddings, err := s.provider.Embed(ctx, []string{text})
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}
	if len(embeddings) == 0 {
		return fmt.Errorf("no embedding returned")
	}

	return s.storeEmbedding(ctx, artifactID, embeddings[0])
}

func (s *EmbeddingService) EmbedArtifacts(ctx context.Context, artifacts []ArtifactText) error {
	for i := 0; i < len(artifacts); i += s.batchSize {
		end := i + s.batchSize
		if end > len(artifacts) {
			end = len(artifacts)
		}
		batch := artifacts[i:end]

		texts := make([]string, len(batch))
		for j, a := range batch {
			texts[j] = a.Text
		}

		embeddings, err := s.provider.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("generate embeddings for batch %d: %w", i/s.batchSize, err)
		}

		for j, a := range batch {
			if j >= len(embeddings) {
				break
			}
			if err := s.storeEmbedding(ctx, a.ArtifactID, embeddings[j]); err != nil {
				slog.Error("failed to store embedding", "artifact_id", a.ArtifactID, "error", err)
				continue
			}
		}

		slog.Info("embedded batch", "batch", i/s.batchSize, "count", len(batch))
	}

	return nil
}

func (s *EmbeddingService) storeEmbedding(ctx context.Context, artifactID string, embedding []float32) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO artifact_embeddings (artifact_id, embedding, model_name)
		 VALUES ($1, $2, 'default')
		 ON CONFLICT DO NOTHING`,
		artifactID, pgvector.NewVector(embedding),
	)
	if err != nil {
		return fmt.Errorf("insert embedding: %w", err)
	}
	return nil
}
