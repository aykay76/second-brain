package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	DB      DBConfig      `yaml:"db"`
	LLM     LLMConfig     `yaml:"llm"`
	Sources SourcesConfig `yaml:"sources"`
}

type SourcesConfig struct {
	Filesystem FilesystemConfig `yaml:"filesystem"`
	GitHub     GitHubConfig     `yaml:"github"`
	ArXiv      ArXivConfig      `yaml:"arxiv"`
}

type GitHubConfig struct {
	Enabled      bool     `yaml:"enabled"`
	Token        string   `yaml:"token"`
	SyncStarred  bool     `yaml:"sync_starred"`
	SyncGists    bool     `yaml:"sync_gists"`
	IncludeRepos []string `yaml:"include_repos"`
}

type ArXivConfig struct {
	Enabled          bool     `yaml:"enabled"`
	Categories       []string `yaml:"categories"`
	Keywords         []string `yaml:"keywords"`
	MaxResults       int      `yaml:"max_results"`
	InitialLookback  string   `yaml:"initial_lookback"`
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

func Load(path string) (*Config, error) {
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
