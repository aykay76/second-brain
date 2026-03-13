package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"pa/internal/tagging"
)

func EnrichHandler(svc *tagging.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("enrichment triggered")

		result, err := svc.Enrich(r.Context())
		if err != nil {
			slog.Error("enrichment failed", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}
