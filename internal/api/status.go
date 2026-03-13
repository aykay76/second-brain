package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
)

type sourceCounts map[string]int
type typeCounts map[string]int

type statusResponse struct {
	Artifacts     artifactStatus    `json:"artifacts"`
	Embeddings    embeddingStatus   `json:"embeddings"`
	Relationships relationshipStats `json:"relationships"`
	SyncCursors   []syncCursor      `json:"sync_cursors"`
}

type artifactStatus struct {
	Total    int          `json:"total"`
	BySource sourceCounts `json:"by_source"`
	ByType   typeCounts   `json:"by_type"`
}

type embeddingStatus struct {
	Total    int     `json:"total"`
	Coverage float64 `json:"coverage"`
}

type relationshipStats struct {
	Total  int        `json:"total"`
	ByType typeCounts `json:"by_type"`
}

type syncCursor struct {
	SourceName  string `json:"source_name"`
	CursorValue string `json:"cursor_value"`
	UpdatedAt   string `json:"updated_at"`
}

func StatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		resp := statusResponse{
			Artifacts: artifactStatus{
				BySource: make(sourceCounts),
				ByType:   make(typeCounts),
			},
			Embeddings:    embeddingStatus{},
			Relationships: relationshipStats{ByType: make(typeCounts)},
			SyncCursors:   []syncCursor{},
		}

		resp.Artifacts.Total = countRows(ctx, db, `SELECT COUNT(*) FROM artifacts`)
		resp.Artifacts.BySource = groupCounts(ctx, db, `SELECT source, COUNT(*) FROM artifacts GROUP BY source ORDER BY COUNT(*) DESC`)
		resp.Artifacts.ByType = groupCounts(ctx, db, `SELECT artifact_type, COUNT(*) FROM artifacts GROUP BY artifact_type ORDER BY COUNT(*) DESC`)
		resp.Embeddings.Total = countRows(ctx, db, `SELECT COUNT(*) FROM artifact_embeddings`)
		resp.Relationships.Total = countRows(ctx, db, `SELECT COUNT(*) FROM relationships`)
		resp.Relationships.ByType = groupCounts(ctx, db, `SELECT relation_type, COUNT(*) FROM relationships GROUP BY relation_type ORDER BY COUNT(*) DESC`)
		resp.SyncCursors = loadSyncCursors(ctx, db)

		if resp.Artifacts.Total > 0 {
			resp.Embeddings.Coverage = float64(resp.Embeddings.Total) / float64(resp.Artifacts.Total) * 100
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func countRows(ctx context.Context, db *sql.DB, query string) int {
	var n int
	db.QueryRowContext(ctx, query).Scan(&n)
	return n
}

func groupCounts(ctx context.Context, db *sql.DB, query string) map[string]int {
	m := make(map[string]int)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int
		if rows.Scan(&key, &count) == nil {
			m[key] = count
		}
	}
	return m
}

func loadSyncCursors(ctx context.Context, db *sql.DB) []syncCursor {
	rows, err := db.QueryContext(ctx, `SELECT source_name, cursor_value, updated_at FROM sync_cursors ORDER BY updated_at DESC`)
	if err != nil {
		return []syncCursor{}
	}
	defer rows.Close()
	var cursors []syncCursor
	for rows.Next() {
		var sc syncCursor
		if rows.Scan(&sc.SourceName, &sc.CursorValue, &sc.UpdatedAt) == nil {
			cursors = append(cursors, sc)
		}
	}
	if cursors == nil {
		return []syncCursor{}
	}
	return cursors
}
