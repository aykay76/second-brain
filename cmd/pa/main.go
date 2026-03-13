package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pa/internal/api"
	"pa/internal/config"
	"pa/internal/database"
	"pa/internal/ingestion/filesystem"
	gh "pa/internal/ingestion/github"
	"pa/internal/llm"
	"pa/internal/retrieval"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := config.Load("config/config.yaml")
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	db, err := database.Connect(ctx, cfg.DB)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := database.Migrate(db); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	provider, err := llm.NewProvider(cfg.LLM)
	if err != nil {
		slog.Error("failed to create llm provider", "error", err)
		os.Exit(1)
	}
	slog.Info("llm provider ready", "provider", cfg.LLM.Provider)

	embeddingSvc := retrieval.NewEmbeddingService(provider.Embedder, db)
	searchSvc := retrieval.NewSearchService(provider.Embedder, db)
	ragSvc := retrieval.NewRAGService(searchSvc, provider.Chat, db)

	fsScanner := filesystem.NewScanner(db, embeddingSvc, cfg.Sources.Filesystem)

	if cfg.Sources.Filesystem.Enabled {
		watcher, err := filesystem.NewWatcher(fsScanner)
		if err != nil {
			slog.Warn("failed to create filesystem watcher, real-time updates disabled", "error", err)
		} else {
			watchCtx, watchCancel := context.WithCancel(ctx)
			defer watchCancel()
			go func() {
				if err := watcher.Start(watchCtx); err != nil {
					slog.Error("filesystem watcher stopped", "error", err)
				}
			}()
			slog.Info("filesystem watcher enabled")
		}
	}

	ghSyncer := gh.NewSyncer(db, embeddingSvc, cfg.Sources.GitHub)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", api.HealthHandler(db))
	mux.HandleFunc("GET /search", api.SearchHandler(searchSvc))
	mux.HandleFunc("POST /ingest/filesystem", api.IngestFilesystemHandler(fsScanner))
	mux.HandleFunc("POST /ingest/github", api.IngestHandler(ghSyncer))
	mux.HandleFunc("POST /ask", api.AskHandler(ragSvc))

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("starting server", "port", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	slog.Info("server stopped")
}
