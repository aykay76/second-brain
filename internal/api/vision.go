package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"pa/internal/ingestion/vision"
)

// VisionIngestHandler starts a background vision ingestion job.
func VisionIngestHandler(jobManager *vision.JobManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("vision ingestion triggered")

		job, err := jobManager.Submit(r.Context())
		if err != nil {
			slog.Error("failed to submit vision job", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "vision ingestion job started",
			"job_id":  job.ID,
			"status":  "GET /ingest/vision/jobs/" + job.ID,
		})

		slog.Info("vision job submitted", "job_id", job.ID)
	}
}

// VisionJobStatusHandler returns the status of a vision ingestion job.
func VisionJobStatusHandler(jobManager *vision.JobManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("id")

		job := jobManager.Get(jobID)
		if job == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		status := job.Status()

		// Set appropriate status code
		if done, ok := status["done"].(bool); ok && !done {
			w.WriteHeader(http.StatusOK)
		} else if err, ok := status["error"].(string); ok && err != "" {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		json.NewEncoder(w).Encode(status)
	}
}
