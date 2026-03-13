package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"pa/internal/retrieval"
)

func AskHandler(svc *retrieval.RAGService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		question, topK := parseAskRequest(r)

		if question == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "question is required (JSON body with 'question' field, or ?q= query parameter)",
			})
			return
		}

		resp, err := svc.Ask(r.Context(), retrieval.AskRequest{
			Question: question,
			TopK:     topK,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func parseAskRequest(r *http.Request) (question string, topK int) {
	if r.Header.Get("Content-Type") == "application/json" {
		var body struct {
			Question string `json:"question"`
			TopK     int    `json:"top_k,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			return body.Question, body.TopK
		}
		return "", 0
	}

	question = r.URL.Query().Get("q")
	if s := r.URL.Query().Get("top_k"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			topK = n
		}
	}
	return question, topK
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
