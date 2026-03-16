package llm

import (
	"fmt"

	"pa/internal/config"
)

const (
	dimensionOllama = 768  // nomic-embed-text
	dimensionOpenAI = 1536 // text-embedding-3-small
	dimensionGroq   = 768  // nomic-embed-text
)

type Provider struct {
	Embedder EmbeddingProvider
	Chat     ChatProvider
	Vision   VisionProvider
}

func NewProvider(cfg config.LLMConfig) (*Provider, error) {
	switch cfg.Provider {
	case "ollama":
		p := NewOllamaProvider(
			cfg.Ollama.BaseURL,
			cfg.Ollama.EmbeddingModel,
			cfg.Ollama.ChatModel,
			cfg.Ollama.VisionModel,
			dimensionOllama,
		)
		return &Provider{Embedder: p, Chat: p, Vision: p}, nil

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
		return &Provider{Embedder: p, Chat: p, Vision: nil}, nil

	case "groq":
		if cfg.Groq.APIKey == "" {
			return nil, fmt.Errorf("groq api_key is required when provider is groq")
		}
		p := NewGroqProvider(
			cfg.Groq.APIKey,
			cfg.Groq.EmbeddingModel,
			cfg.Groq.ChatModel,
			cfg.Groq.VisionModel,
			dimensionGroq,
		)
		return &Provider{Embedder: p, Chat: p, Vision: p}, nil

	default:
		return nil, fmt.Errorf("unknown llm provider: %q (expected ollama, openai, or groq)", cfg.Provider)
	}
}
