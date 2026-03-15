package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	DB         DBConfig         `yaml:"db"`
	LLM        LLMConfig        `yaml:"llm"`
	Sources    SourcesConfig    `yaml:"sources"`
	Discovery  DiscoveryConfig  `yaml:"discovery"`
	Enrichment EnrichmentConfig `yaml:"enrichment"`
	Digest     DigestConfig     `yaml:"digest"`
	Insights   InsightsConfig   `yaml:"insights"`
}

type SourcesConfig struct {
	Filesystem  FilesystemConfig  `yaml:"filesystem"`
	GitHub      GitHubConfig      `yaml:"github"`
	ArXiv       ArXivConfig       `yaml:"arxiv"`
	Trending    TrendingConfig    `yaml:"trending"`
	YouTube     YouTubeConfig     `yaml:"youtube"`
	OneDrive    OneDriveConfig    `yaml:"onedrive"`
	TheNewStack TheNewStackConfig `yaml:"thenewstack"`
}

type DiscoveryConfig struct {
	Enabled             bool    `yaml:"enabled"`
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
	MaxCandidates       int     `yaml:"max_candidates"`
	BatchSize           int     `yaml:"batch_size"`
}

type EnrichmentConfig struct {
	Enabled   bool `yaml:"enabled"`
	BatchSize int  `yaml:"batch_size"`
	MaxTags   int  `yaml:"max_tags"`
}

type DigestConfig struct {
	DefaultPeriod string `yaml:"default_period"`
	WeekStartDay  string `yaml:"week_start_day"`
}

type InsightsConfig struct {
	Enabled              bool    `yaml:"enabled"`
	GemsLookbackDays     int     `yaml:"gems_lookback_days"`
	SerendipityLimit     int     `yaml:"serendipity_limit"`
	TopicWindowWeeks     int     `yaml:"topic_window_weeks"`
	DepthMinArtifacts    int     `yaml:"depth_min_artifacts"`
	VelocityRollingWeeks int     `yaml:"velocity_rolling_weeks"`
	SimilarityThreshold  float64 `yaml:"similarity_threshold"`
}

type GitHubConfig struct {
	Enabled      bool     `yaml:"enabled"`
	Token        string   `yaml:"token"`
	SyncStarred  bool     `yaml:"sync_starred"`
	SyncGists    bool     `yaml:"sync_gists"`
	IncludeRepos []string `yaml:"include_repos"`
}

type ArXivConfig struct {
	Enabled         bool     `yaml:"enabled"`
	Categories      []string `yaml:"categories"`
	Keywords        []string `yaml:"keywords"`
	MaxResults      int      `yaml:"max_results"`
	InitialLookback string   `yaml:"initial_lookback"`
}

type TrendingConfig struct {
	Enabled     bool     `yaml:"enabled"`
	Languages   []string `yaml:"languages"`
	FetchReadme bool     `yaml:"fetch_readme"`
}

type TheNewStackConfig struct {
	Enabled     bool `yaml:"enabled"`
	MaxArticles int  `yaml:"max_articles"`
}

type YouTubeConfig struct {
	Enabled     bool     `yaml:"enabled"`
	APIKey      string   `yaml:"api_key"`
	Channels    []string `yaml:"channels"`
	SearchTerms []string `yaml:"search_terms"`
	MaxResults  int      `yaml:"max_results"`
}

type OneDriveConfig struct {
	Enabled    bool     `yaml:"enabled"`
	ClientID   string   `yaml:"client_id"`
	TenantID   string   `yaml:"tenant_id"`
	Folders    []string `yaml:"folders"`
	Extensions []string `yaml:"extensions"`
	TokenFile  string   `yaml:"token_file"`
}

type FilesystemConfig struct {
	Enabled    bool     `yaml:"enabled"`
	Paths      []string `yaml:"paths"`
	Extensions []string `yaml:"extensions"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type DBConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	Name         string `yaml:"name"`
	User         string `yaml:"user"`
	Password     string `yaml:"password"`
	SSLMode      string `yaml:"sslmode"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

func (c DBConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		c.Host, c.Port, c.Name, c.User, c.Password, c.SSLMode,
	)
}

type LLMConfig struct {
	Provider string       `yaml:"provider"`
	Ollama   OllamaConfig `yaml:"ollama"`
	OpenAI   OpenAIConfig `yaml:"openai"`
	Groq     GroqConfig   `yaml:"groq"`
}

type OllamaConfig struct {
	BaseURL        string `yaml:"base_url"`
	EmbeddingModel string `yaml:"embedding_model"`
	ChatModel      string `yaml:"chat_model"`
}

type OpenAIConfig struct {
	APIKey         string `yaml:"api_key"`
	EmbeddingModel string `yaml:"embedding_model"`
	ChatModel      string `yaml:"chat_model"`
}

type GroqConfig struct {
	APIKey         string `yaml:"api_key"`
	EmbeddingModel string `yaml:"embedding_model"`
	ChatModel      string `yaml:"chat_model"`
}

func Load(path string) (*Config, error) {
	// Load .env files from current directory and parent directories
	loadEnvFiles()

	if envPath := os.Getenv("PA_CONFIG_PATH"); envPath != "" {
		path = envPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Server.Port <= 0 {
		return fmt.Errorf("server.port must be positive")
	}
	if c.DB.Host == "" {
		return fmt.Errorf("db.host is required")
	}
	if c.DB.Name == "" {
		return fmt.Errorf("db.name is required")
	}
	return nil
}

// loadEnvFiles loads environment variables from .env and .env.local files.
// It searches for these files in the current working directory.
// Environment variables already set are not overwritten.
func loadEnvFiles() {
	envFiles := []string{".env", ".env.local"}
	for _, filename := range envFiles {
		if err := parseEnvFile(filename); err == nil {
			// File loaded successfully, continue to next
		}
		// Continue even if file doesn't exist or has errors
	}
}

// parseEnvFile reads a .env file and sets environment variables.
// Lines starting with # are treated as comments.
// Format: KEY=VALUE or KEY="VALUE" or KEY='VALUE'
// Empty lines are ignored.
// Environment variables already set are not overwritten.
func parseEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err // Silently fail if file doesn't exist
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
			(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
			value = value[1 : len(value)-1]
		}

		// Only set if not already in environment
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}
