package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"pa/internal/retrieval"
)

// webFS holds the filesystem for embedded web files
var webFS fs.FS

// SetWebFS sets the embedded filesystem for web files
func SetWebFS(f fs.FS) {
	webFS = f
}

// ServeIndexHandler returns a handler that serves the index.html
func ServeIndexHandler(fsys fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Serve index.html for root path
		if r.URL.Path == "/" {
			data, err := fs.ReadFile(fsys, "index.html")
			if err != nil {
				http.Error(w, "Failed to load index", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Write(data)
			return
		}

		// For any other path, redirect to index (SPA behavior)
		if !strings.Contains(r.URL.Path, ".") {
			data, err := fs.ReadFile(fsys, "index.html")
			if err != nil {
				http.Error(w, "Failed to load index", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
			return
		}

		http.NotFound(w, r)
	}
}

// ServeStaticHandler returns a handler for serving static files (CSS, JS, etc.)
func ServeStaticHandler(dir string, fsys fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fsys == nil {
			http.Error(w, "Web filesystem not initialized", http.StatusInternalServerError)
			return
		}

		// Extract the file path from the URL - keep the full path
		filePath := strings.TrimPrefix(r.URL.Path, "/")
		if filePath == "" {
			http.NotFound(w, r)
			return
		}

		// Prevent directory traversal
		if strings.Contains(filePath, "..") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Read the file from the embedded filesystem
		data, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Set appropriate content type
		switch {
		case strings.HasSuffix(filePath, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case strings.HasSuffix(filePath, ".js"):
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case strings.HasSuffix(filePath, ".png"):
			w.Header().Set("Content-Type", "image/png")
		case strings.HasSuffix(filePath, ".jpg") || strings.HasSuffix(filePath, ".jpeg"):
			w.Header().Set("Content-Type", "image/jpeg")
		case strings.HasSuffix(filePath, ".svg"):
			w.Header().Set("Content-Type", "image/svg+xml")
		case strings.HasSuffix(filePath, ".woff"):
			w.Header().Set("Content-Type", "font/woff")
		case strings.HasSuffix(filePath, ".woff2"):
			w.Header().Set("Content-Type", "font/woff2")
		}

		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(data)
	}
}

// ServeWeb returns an http.Handler for serving the web interface
func ServeWeb() http.Handler {
	// Return a multiplexer that serves the web UI
	// This is provided for backwards compatibility
	mux := http.NewServeMux()

	if webFS != nil {
		mux.HandleFunc("GET /", ServeIndexHandler(webFS))
		mux.HandleFunc("GET /css/", ServeStaticHandler("css", webFS))
		mux.HandleFunc("GET /js/", ServeStaticHandler("js", webFS))
	}

	return mux
}

// ChatAskRequest represents a chat ask request
type ChatAskRequest struct {
	Question string `json:"question"`
	TopK     int    `json:"top_k,omitempty"`
}

// ChatAskResponse represents a chat ask response
type ChatAskResponse struct {
	Answer  string             `json:"answer"`
	Sources []retrieval.Source `json:"sources,omitempty"`
}

// ChatAskHandler returns a handler for chat-based ask requests
func ChatAskHandler(ragSvc *retrieval.RAGService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatAskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Invalid request body",
			})
			return
		}

		if req.Question == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "question is required",
			})
			return
		}

		if req.TopK == 0 {
			req.TopK = 5
		}

		resp, err := ragSvc.Ask(r.Context(), retrieval.AskRequest{
			Question: req.Question,
			TopK:     req.TopK,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		fmt.Println("Response: ", resp)

		// Extract sources from the response if available
		sources := []string{}
		if resp.Sources != nil && len(resp.Sources) > 0 {
			for _, src := range resp.Sources {
				sourceStr := src.Title
				if sourceStr == "" {
					sourceStr = src.ArtifactID
				}
				if sourceStr != "" {
					sources = append(sources, sourceStr)
				}
			}
		}

		writeJSON(w, http.StatusOK, ChatAskResponse{
			Answer:  resp.Answer,
			Sources: resp.Sources,
		})
	}
}

// For now, we'll use a workaround - the web will use the existing /status endpoint
// This is fine since the format is compatible
