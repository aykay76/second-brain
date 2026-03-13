package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

type healthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

func HealthHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbStatus := "up"
		status := "ok"
		httpStatus := http.StatusOK

		if err := db.PingContext(r.Context()); err != nil {
			dbStatus = "down"
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		json.NewEncoder(w).Encode(healthResponse{
			Status:   status,
			Database: dbStatus,
		})
	}
}
