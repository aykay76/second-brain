CREATE EXTENSION IF NOT EXISTS vector;

-- Artifacts: the universal record for any piece of knowledge.
CREATE TABLE artifacts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source        TEXT NOT NULL,
    artifact_type TEXT NOT NULL,
    external_id   TEXT NOT NULL,
    title         TEXT NOT NULL,
    content       TEXT,
    summary       TEXT,
    metadata      JSONB NOT NULL DEFAULT '{}',
    content_hash  TEXT,
    source_url    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ingested_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_artifacts_source_external_id ON artifacts (source, external_id);
CREATE INDEX idx_artifacts_source ON artifacts (source);
CREATE INDEX idx_artifacts_type ON artifacts (artifact_type);
CREATE INDEX idx_artifacts_created_at ON artifacts (created_at);

-- Full-text search via generated tsvector column.
ALTER TABLE artifacts ADD COLUMN tsv tsvector
    GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(title, '') || ' ' || coalesce(content, ''))
    ) STORED;

CREATE INDEX idx_artifacts_tsv ON artifacts USING GIN (tsv);

-- Artifact embeddings with pgvector HNSW index.
-- Default dimension 768 matches Ollama nomic-embed-text.
CREATE TABLE artifact_embeddings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id UUID NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    embedding   vector(768),
    model_name  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_embeddings_artifact_id ON artifact_embeddings (artifact_id);
CREATE INDEX idx_embeddings_hnsw ON artifact_embeddings
    USING hnsw (embedding vector_cosine_ops);

-- Relationships between artifacts.
CREATE TABLE relationships (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id     UUID NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    target_id     UUID NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL,
    confidence    REAL NOT NULL DEFAULT 1.0,
    metadata      JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_id, target_id, relation_type)
);

CREATE INDEX idx_relationships_source_id ON relationships (source_id);
CREATE INDEX idx_relationships_target_id ON relationships (target_id);
CREATE INDEX idx_relationships_type ON relationships (relation_type);

-- Sync cursors for incremental ingestion per source.
CREATE TABLE sync_cursors (
    source_name  TEXT PRIMARY KEY,
    cursor_value TEXT NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Tags for user-defined and auto-generated labels.
CREATE TABLE tags (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id    UUID NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    tag            TEXT NOT NULL,
    auto_generated BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (artifact_id, tag)
);

CREATE INDEX idx_tags_artifact_id ON tags (artifact_id);
CREATE INDEX idx_tags_tag ON tags (tag);
