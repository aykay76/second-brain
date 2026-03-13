package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type artifact struct {
	ID           string          `json:"id"`
	Source       string          `json:"source"`
	ArtifactType string         `json:"artifact_type"`
	Title        string          `json:"title"`
	Content      *string         `json:"content,omitempty"`
	Summary      *string         `json:"summary,omitempty"`
	SourceURL    *string         `json:"source_url,omitempty"`
	Metadata     json.RawMessage `json:"metadata"`
	CreatedAt    string          `json:"created_at"`
	IngestedAt   string          `json:"ingested_at"`
}

type artifactListResponse struct {
	Count     int        `json:"count"`
	Artifacts []artifact `json:"artifacts"`
}

type artifactFilter struct {
	source       string
	artifactType string
	from         string
	to           string
	limit        int
	orderBy      string
}

func parseArtifactFilter(r *http.Request) artifactFilter {
	f := artifactFilter{
		source:       r.URL.Query().Get("source"),
		artifactType: r.URL.Query().Get("type"),
		from:         r.URL.Query().Get("from"),
		to:           r.URL.Query().Get("to"),
		limit:        20,
		orderBy:      "ingested_at DESC",
	}

	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		f.limit = n
	}

	switch r.URL.Query().Get("sort") {
	case "created":
		f.orderBy = "created_at DESC"
	case "title":
		f.orderBy = "title ASC"
	}

	return f
}

func (f artifactFilter) buildQuery() (string, []any) {
	var conditions []string
	var args []any
	argIdx := 1

	if f.source != "" {
		conditions = append(conditions, fmt.Sprintf("source = $%d", argIdx))
		args = append(args, f.source)
		argIdx++
	}
	if f.artifactType != "" {
		conditions = append(conditions, fmt.Sprintf("artifact_type = $%d", argIdx))
		args = append(args, f.artifactType)
		argIdx++
	}
	if f.from != "" {
		conditions = append(conditions, fmt.Sprintf("ingested_at >= $%d", argIdx))
		args = append(args, f.from)
		argIdx++
	}
	if f.to != "" {
		conditions = append(conditions, fmt.Sprintf("ingested_at < $%d", argIdx))
		args = append(args, f.to)
		argIdx++
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT id, source, artifact_type, title, content, summary, source_url, metadata,
			created_at::text, ingested_at::text
		FROM artifacts %s
		ORDER BY %s
		LIMIT $%d
	`, where, f.orderBy, argIdx)
	args = append(args, f.limit)

	return query, args
}

func ListArtifactsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := parseArtifactFilter(r)
		query, args := filter.buildQuery()

		rows, err := db.QueryContext(r.Context(), query, args...)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()

		artifacts := scanArtifacts(rows)
		if artifacts == nil {
			artifacts = []artifact{}
		}

		writeJSON(w, http.StatusOK, artifactListResponse{
			Count:     len(artifacts),
			Artifacts: artifacts,
		})
	}
}

func scanArtifacts(rows *sql.Rows) []artifact {
	var artifacts []artifact
	for rows.Next() {
		var a artifact
		if rows.Scan(
			&a.ID, &a.Source, &a.ArtifactType, &a.Title,
			&a.Content, &a.Summary, &a.SourceURL, &a.Metadata,
			&a.CreatedAt, &a.IngestedAt,
		) == nil {
			a.Content = nil
			artifacts = append(artifacts, a)
		}
	}
	return artifacts
}

type relatedArtifact struct {
	Artifact     artifact `json:"artifact"`
	RelationType string   `json:"relation_type"`
	Confidence   float64  `json:"confidence"`
}

type relatedResponse struct {
	Artifact artifact          `json:"artifact"`
	Related  []relatedArtifact `json:"related"`
}

func RelatedHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "artifact id is required"})
			return
		}

		a, err := findArtifact(r.Context(), db, id)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		related, err := findRelated(r.Context(), db, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		a.Content = nil
		writeJSON(w, http.StatusOK, relatedResponse{Artifact: *a, Related: related})
	}
}

func findArtifact(ctx context.Context, db *sql.DB, id string) (*artifact, error) {
	var a artifact
	err := db.QueryRowContext(ctx, `
		SELECT id, source, artifact_type, title, content, summary, source_url, metadata,
			created_at::text, ingested_at::text
		FROM artifacts WHERE id = $1
	`, id).Scan(
		&a.ID, &a.Source, &a.ArtifactType, &a.Title,
		&a.Content, &a.Summary, &a.SourceURL, &a.Metadata,
		&a.CreatedAt, &a.IngestedAt,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func findRelated(ctx context.Context, db *sql.DB, id string) ([]relatedArtifact, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.source, a.artifact_type, a.title, a.content, a.summary,
			a.source_url, a.metadata, a.created_at::text, a.ingested_at::text,
			r.relation_type, COALESCE(r.confidence, 0)
		FROM relationships r
		JOIN artifacts a ON (
			(a.id = r.target_id AND r.source_id = $1)
			OR (a.id = r.source_id AND r.target_id = $1)
		)
		WHERE a.id != $1
		ORDER BY r.confidence DESC NULLS LAST
		LIMIT 20
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRelated(rows)
}

func scanRelated(rows *sql.Rows) ([]relatedArtifact, error) {
	var related []relatedArtifact
	for rows.Next() {
		var ra relatedArtifact
		if err := rows.Scan(
			&ra.Artifact.ID, &ra.Artifact.Source, &ra.Artifact.ArtifactType,
			&ra.Artifact.Title, &ra.Artifact.Content, &ra.Artifact.Summary,
			&ra.Artifact.SourceURL, &ra.Artifact.Metadata,
			&ra.Artifact.CreatedAt, &ra.Artifact.IngestedAt,
			&ra.RelationType, &ra.Confidence,
		); err != nil {
			return nil, err
		}
		ra.Artifact.Content = nil
		related = append(related, ra)
	}
	if related == nil {
		related = []relatedArtifact{}
	}
	return related, nil
}

func TagHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "artifact id is required"})
			return
		}

		var exists bool
		if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM artifacts WHERE id = $1)`, id).Scan(&exists); err != nil || !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
			return
		}

		var body struct {
			Tag string `json:"tag"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Tag == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag field is required"})
			return
		}

		var tagID string
		err := db.QueryRowContext(r.Context(), `
			INSERT INTO tags (id, artifact_id, tag, auto_generated)
			VALUES (gen_random_uuid(), $1, $2, false)
			ON CONFLICT DO NOTHING
			RETURNING id
		`, id, body.Tag).Scan(&tagID)

		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusOK, map[string]string{
				"message":     "tag already exists",
				"artifact_id": id,
				"tag":         body.Tag,
			})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{
			"id":          tagID,
			"artifact_id": id,
			"tag":         body.Tag,
		})
	}
}
