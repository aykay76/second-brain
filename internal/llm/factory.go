package llm

import (
	"fmt"

	"pa/internal/config"
)

const (
	dimensionOllama = 768  // nomic-embed-text
	dimensionOpenAI = 1536 // text-embedding-3-small
)

type Provider struct {
	Embedder EmbeddingProvider
	Chat     ChatProvider
}

func NewProvider(cfg config.LLMConfig) (*Provider, error) {
	switch cfg.Provider {
	case "ollama":
		p := NewOllamaProvider(
			cfg.Ollama.BaseURL,
			cfg.Ollama.EmbeddingModel,
			cfg.Ollama.ChatModel,
			dimensionOllama,
		)
		return &Provider{Embedder: p, Chat: p}, nil

	case "openai":
		if cfg.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("openai api_key is required when provider is openai")
		}
		p := NewOpenAIProvider(
			cfg.OpenAI.APIKey,
			cfg.OpenAI.EmbeddingModel,
			cfg.OpenAI.ChatModel,
			dimensionOpenAI,
		)
		return &Provider{Embedder: p, Chat: p}, nil

	default:
		return nil, fmt.Errorf("unknown llm provider: %q (expected ollama or openai)", cfg.Provider)
	}
}
