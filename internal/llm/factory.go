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
	// Determine which providers to use
	embeddingProvider := cfg.EmbeddingProvider
	visionProvider := cfg.VisionProvider
	chatProvider := cfg.Provider // Default chat provider

	// If specific providers aren't set, fall back to default provider
	if embeddingProvider == "" {
		embeddingProvider = cfg.Provider
	}
	if visionProvider == "" {
		visionProvider = cfg.Provider
	}

	// Create embedding provider
	embedder, err := createEmbeddingProvider(embeddingProvider, cfg)
	if err != nil {
		return nil, err
	}

	// Create chat provider
	chat, err := createChatProvider(chatProvider, cfg)
	if err != nil {
		return nil, err
	}

	// Create vision provider
	vision, err := createVisionProvider(visionProvider, cfg)
	if err != nil {
		return nil, err
	}

	return &Provider{Embedder: embedder, Chat: chat, Vision: vision}, nil
}

func createEmbeddingProvider(provider string, cfg config.LLMConfig) (EmbeddingProvider, error) {
	switch provider {
	case "ollama":
		p := NewOllamaProvider(
			cfg.Ollama.BaseURL,
			cfg.Ollama.EmbeddingModel,
			cfg.Ollama.ChatModel,
			cfg.Ollama.VisionModel,
			dimensionOllama,
		)
		return p, nil
	case "openai":
		if cfg.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("openai api_key is required when provider is openai")
		}
		p := NewOpenAIProvider(
			cfg.OpenAI.BaseURL,
			cfg.OpenAI.APIKey,
			cfg.OpenAI.EmbeddingModel,
			cfg.OpenAI.ChatModel,
			cfg.OpenAI.VisionModel,
			dimensionOpenAI,
		)
		return p, nil
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
		return p, nil
	default:
		return nil, fmt.Errorf("unknown embedding provider: %q (expected ollama, openai, or groq)", provider)
	}
}

func createChatProvider(provider string, cfg config.LLMConfig) (ChatProvider, error) {
	switch provider {
	case "ollama":
		p := NewOllamaProvider(
			cfg.Ollama.BaseURL,
			cfg.Ollama.EmbeddingModel,
			cfg.Ollama.ChatModel,
			cfg.Ollama.VisionModel,
			dimensionOllama,
		)
		return p, nil
	case "openai":
		if cfg.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("openai api_key is required when provider is openai")
		}
		p := NewOpenAIProvider(
			cfg.OpenAI.BaseURL,
			cfg.OpenAI.APIKey,
			cfg.OpenAI.EmbeddingModel,
			cfg.OpenAI.ChatModel,
			cfg.OpenAI.VisionModel,
			dimensionOpenAI,
		)
		return p, nil
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
		return p, nil
	default:
		return nil, fmt.Errorf("unknown chat provider: %q (expected ollama, openai, or groq)", provider)
	}
}

func createVisionProvider(provider string, cfg config.LLMConfig) (VisionProvider, error) {
	switch provider {
	case "ollama":
		p := NewOllamaProvider(
			cfg.Ollama.BaseURL,
			cfg.Ollama.EmbeddingModel,
			cfg.Ollama.ChatModel,
			cfg.Ollama.VisionModel,
			dimensionOllama,
		)
		return p, nil
	case "openai":
		if cfg.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("openai api_key is required when provider is openai")
		}
		p := NewOpenAIProvider(
			cfg.OpenAI.BaseURL,
			cfg.OpenAI.APIKey,
			cfg.OpenAI.EmbeddingModel,
			cfg.OpenAI.ChatModel,
			cfg.OpenAI.VisionModel,
			dimensionOpenAI,
		)
		return p, nil
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
		return p, nil
	default:
		return nil, fmt.Errorf("unknown vision provider: %q (expected ollama, openai, or groq)", provider)
	}
}
