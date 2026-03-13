package api

import (
	"log/slog"
	"net/http"

	"pa/internal/digest"
)

func DigestHandler(svc *digest.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		req := digest.DigestRequest{
			NaturalTZ: q.Get("natural"),
		}

		if p := q.Get("period"); p != "" {
			period, err := digest.ParsePeriod(p)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			req.Period = period
		}

		if from := q.Get("from"); from != "" {
			req.From = &from
		}
		if to := q.Get("to"); to != "" {
			req.To = &to
		}

		slog.Info("digest requested",
			"period", req.Period,
			"from", req.From,
			"to", req.To,
			"natural", req.NaturalTZ,
		)

		resp, err := svc.Generate(r.Context(), req)
		if err != nil {
			slog.Error("digest generation failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
