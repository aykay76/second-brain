package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"pa/internal/retrieval"
)

type searchResponse struct {
	Query   string                   `json:"query"`
	Count   int                      `json:"count"`
	Results []retrieval.SearchResult `json:"results"`
}

func SearchHandler(svc *retrieval.SearchService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, `{"error": "q parameter is required"}`, http.StatusBadRequest)
			return
		}

		opts := retrieval.DefaultSearchOptions()
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
				opts.Limit = limit
			}
		}

		if tagsParam := r.URL.Query().Get("tags"); tagsParam != "" {
			for _, t := range strings.Split(tagsParam, ",") {
				if tag := strings.TrimSpace(t); tag != "" {
					opts.Tags = append(opts.Tags, tag)
				}
			}
		}

		mode := r.URL.Query().Get("mode")

		var (
			results []retrieval.SearchResult
			err     error
		)

		switch mode {
		case "semantic":
			results, err = svc.SemanticSearch(r.Context(), query, opts.Limit)
		default:
			results, err = svc.Search(r.Context(), query, opts)
		}

		if err != nil {
			http.Error(w, `{"error": "`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searchResponse{
			Query:   query,
			Count:   len(results),
			Results: results,
		})
	}
}
