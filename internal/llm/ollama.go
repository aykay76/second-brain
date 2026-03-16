package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type OllamaProvider struct {
	baseURL        string
	embeddingModel string
	chatModel      string
	dimension      int
	client         *http.Client
}

func NewOllamaProvider(baseURL, embeddingModel, chatModel string, dimension int) *OllamaProvider {
	return &OllamaProvider{
		baseURL:        baseURL,
		embeddingModel: embeddingModel,
		chatModel:      chatModel,
		dimension:      dimension,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (p *OllamaProvider) Dimension() int {
	return p.dimension
}

// --- Embedding ---

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (p *OllamaProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body := ollamaEmbedRequest{
		Model: p.embeddingModel,
		Input: texts,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/embed", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	return result.Embeddings, nil
}

// --- Chat ---

type ollamaChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type ollamaChatResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

// For vision requests, message format includes base64 images in messages.
type ollamaVisionMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // base64-encoded images
}

type ollamaVisionRequest struct {
	Model    string                `json:"model"`
	Messages []ollamaVisionMessage `json:"messages"`
	Stream   bool                  `json:"stream"`
}

func (p *OllamaProvider) Complete(ctx context.Context, messages []Message) (string, error) {
	body := ollamaChatRequest{
		Model:    p.chatModel,
		Messages: messages,
		Stream:   false,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("chat returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}

	return result.Message.Content, nil
}

// Vision sends a request to Ollama with vision capabilities (e.g., llama3.2-vision).
func (p *OllamaProvider) Vision(ctx context.Context, messages []VisionMessage) (string, error) {
	// Convert VisionMessage to ollamaVisionMessage format
	ollamaMessages := make([]ollamaVisionMessage, len(messages))
	for i, msg := range messages {
		ollamaMessages[i] = ollamaVisionMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
		// If there's an image, add it to the images array
		if msg.ImageBase64 != "" {
			ollamaMessages[i].Images = []string{msg.ImageBase64}
		}
	}

	body := ollamaVisionRequest{
		Model:    p.chatModel,
		Messages: ollamaMessages,
		Stream:   false,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal vision request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create vision request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vision returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode vision response: %w", err)
	}

	return result.Message.Content, nil
}
