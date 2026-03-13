package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"pa/internal/digest"
	"pa/internal/insights"
)

func GemsHandler(svc *insights.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lookback := 0
		if v := r.URL.Query().Get("lookback"); v != "" {
			if d, err := strconv.Atoi(v); err == nil {
				lookback = d
			}
		}

		resp, err := svc.ForgottenGems(r.Context(), lookback)
		if err != nil {
			slog.Error("gems failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func SerendipityHandler(svc *insights.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tr, err := resolveInsightTimeRange(q.Get("period"), q.Get("natural"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		resp, err := svc.Serendipity(r.Context(), tr)
		if err != nil {
			slog.Error("serendipity failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func TopicsHandler(svc *insights.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		weeks := 0
		if v := r.URL.Query().Get("weeks"); v != "" {
			if w, err := strconv.Atoi(v); err == nil {
				weeks = w
			}
		}

		resp, err := svc.TopicMomentum(r.Context(), weeks)
		if err != nil {
			slog.Error("topics failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func DepthHandler(svc *insights.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := svc.KnowledgeDepth(r.Context())
		if err != nil {
			slog.Error("depth failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func MemoriesHandler(svc *insights.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := svc.Memories(r.Context(), nil)
		if err != nil {
			slog.Error("memories failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func VelocityHandler(svc *insights.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tr, err := resolveInsightTimeRange(q.Get("period"), q.Get("natural"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		resp, err := svc.LearningVelocity(r.Context(), tr)
		if err != nil {
			slog.Error("velocity failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func resolveInsightTimeRange(period, natural string) (digest.TimeRange, error) {
	n := time.Now().UTC()
	if natural != "" {
		return digest.ParseNaturalDate(natural, n)
	}
	if period != "" {
		p, err := digest.ParsePeriod(period)
		if err != nil {
			return digest.TimeRange{}, err
		}
		return digest.ResolvePeriod(p, n), nil
	}
	return digest.ResolvePeriod(digest.PeriodWeekly, n), nil
}
