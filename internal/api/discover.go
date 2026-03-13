package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"pa/internal/discovery"
)

func DiscoverHandler(engine *discovery.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("discovery triggered")

		result, err := engine.Run(r.Context())
		if err != nil {
			slog.Error("discovery failed", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}
