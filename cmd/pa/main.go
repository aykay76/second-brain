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
	"pa/internal/digest"
	"pa/internal/discovery"
	"pa/internal/ingestion/arxiv"
	"pa/internal/ingestion/filesystem"
	gh "pa/internal/ingestion/github"
	"pa/internal/ingestion/onedrive"
	"pa/internal/ingestion/trending"
	"pa/internal/ingestion/youtube"
	"pa/internal/insights"
	"pa/internal/llm"
	"pa/internal/retrieval"
	"pa/internal/tagging"
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
	arxivSyncer := arxiv.NewSyncer(db, embeddingSvc, cfg.Sources.ArXiv)
	trendingSyncer := trending.NewSyncer(db, embeddingSvc, cfg.Sources.Trending, cfg.Sources.GitHub.Token)
	youtubeSyncer := youtube.NewSyncer(db, embeddingSvc, cfg.Sources.YouTube)
	onedriveSyncer := onedrive.NewSyncer(db, embeddingSvc, cfg.Sources.OneDrive)

	discoveryEngine := discovery.NewEngine(db, cfg.Discovery)

	enrichSvc := tagging.NewService(provider.Chat, db, tagging.Config{
		BatchSize: cfg.Enrichment.BatchSize,
		MaxTags:   cfg.Enrichment.MaxTags,
	})

	digestSvc := digest.NewService(db, provider.Chat, digest.Config{
		DefaultPeriod: digest.Period(cfg.Digest.DefaultPeriod),
		WeekStartDay:  cfg.Digest.WeekStartDay,
	})

	insightsSvc := insights.NewService(db, provider.Embedder, insights.Config{
		GemsLookbackDays:     cfg.Insights.GemsLookbackDays,
		SerendipityLimit:     cfg.Insights.SerendipityLimit,
		TopicWindowWeeks:     cfg.Insights.TopicWindowWeeks,
		DepthMinArtifacts:    cfg.Insights.DepthMinArtifacts,
		VelocityRollingWeeks: cfg.Insights.VelocityRollingWeeks,
		SimilarityThreshold:  cfg.Insights.SimilarityThreshold,
	})

	digestSvc.SetInsights(insightsSvc)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", api.HealthHandler(db))
	mux.HandleFunc("GET /status", api.StatusHandler(db))
	mux.HandleFunc("GET /search", api.SearchHandler(searchSvc))
	mux.HandleFunc("GET /artifacts", api.ListArtifactsHandler(db))
	mux.HandleFunc("GET /artifacts/{id}/related", api.RelatedHandler(db))
	mux.HandleFunc("POST /artifacts/{id}/tags", api.TagHandler(db))
	mux.HandleFunc("POST /ingest/filesystem", api.IngestFilesystemHandler(fsScanner))
	mux.HandleFunc("POST /ingest/github", api.IngestHandler(ghSyncer))
	mux.HandleFunc("POST /ingest/arxiv", api.IngestHandler(arxivSyncer))
	mux.HandleFunc("POST /ingest/trending", api.IngestHandler(trendingSyncer))
	mux.HandleFunc("POST /ingest/youtube", api.IngestHandler(youtubeSyncer))
	mux.HandleFunc("POST /ingest/onedrive", api.IngestHandler(onedriveSyncer))
	mux.HandleFunc("POST /ask", api.AskHandler(ragSvc))
	mux.HandleFunc("POST /discover", api.DiscoverHandler(discoveryEngine))
	mux.HandleFunc("POST /enrich", api.EnrichHandler(enrichSvc))
	mux.HandleFunc("GET /digest", api.DigestHandler(digestSvc))
	mux.HandleFunc("GET /insights/gems", api.GemsHandler(insightsSvc))
	mux.HandleFunc("GET /insights/serendipity", api.SerendipityHandler(insightsSvc))
	mux.HandleFunc("GET /insights/topics", api.TopicsHandler(insightsSvc))
	mux.HandleFunc("GET /insights/depth", api.DepthHandler(insightsSvc))
	mux.HandleFunc("GET /insights/velocity", api.VelocityHandler(insightsSvc))
	mux.HandleFunc("GET /insights/memories", api.MemoriesHandler(insightsSvc))

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
