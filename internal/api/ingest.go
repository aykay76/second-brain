package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"pa/internal/ingestion"
	"pa/internal/ingestion/filesystem"
)

type ingestResponse struct {
	Source   string `json:"source"`
	Ingested int    `json:"ingested"`
	Skipped  int    `json:"skipped"`
	Errors   int    `json:"errors"`
}

func IngestFilesystemHandler(scanner *filesystem.Scanner) http.HandlerFunc {
	return ingestHandler("filesystem", scanner)
}

func IngestHandler(syncer ingestion.Syncer) http.HandlerFunc {
	return ingestHandler(syncer.Name(), syncer)
}

func ingestHandler(source string, syncer ingestion.Syncer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("ingestion triggered", "source", source)

		result, err := syncer.Sync(r.Context())
		if err != nil {
			slog.Error("ingestion failed", "source", source, "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ingestResponse{
			Source:   source,
			Ingested: result.Ingested,
			Skipped:  result.Skipped,
			Errors:   result.Errors,
		})
	}
}
