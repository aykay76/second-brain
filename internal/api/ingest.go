package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"pa/internal/ingestion/filesystem"
)

type ingestResponse struct {
	Source   string `json:"source"`
	Ingested int    `json:"ingested"`
	Skipped  int    `json:"skipped"`
	Errors   int    `json:"errors"`
}

func IngestFilesystemHandler(scanner *filesystem.Scanner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("filesystem ingestion triggered")

		result, err := scanner.Sync(r.Context())
		if err != nil {
			slog.Error("filesystem ingestion failed", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ingestResponse{
			Source:   "filesystem",
			Ingested: result.Ingested,
			Skipped:  result.Skipped,
			Errors:   result.Errors,
		})
	}
}
