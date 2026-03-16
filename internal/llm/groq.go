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

type GroqProvider struct {
	apiKey         string
	embeddingModel string
	chatModel      string
	dimension      int
	client         *http.Client
}

func NewGroqProvider(apiKey, embeddingModel, chatModel string, dimension int) *GroqProvider {
	return &GroqProvider{
		apiKey:         apiKey,
		embeddingModel: embeddingModel,
		chatModel:      chatModel,
		dimension:      dimension,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (p *GroqProvider) Dimension() int {
	return p.dimension
}

// --- Embedding ---

type groqEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type groqEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (p *GroqProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body := groqEmbedRequest{
		Model: p.embeddingModel,
		Input: texts,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result groqEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	embeddings := make([][]float32, len(result.Data))
	for _, d := range result.Data {
		embeddings[d.Index] = d.Embedding
	}

	return embeddings, nil
}

// --- Chat ---

type groqChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type groqChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// For vision, we need to support content as either string or array of objects
type groqVisionContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

type groqVisionMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // Can be string or []groqVisionContent
}

type groqVisionRequest struct {
	Model    string              `json:"model"`
	Messages []groqVisionMessage `json:"messages"`
}

func (p *GroqProvider) Complete(ctx context.Context, messages []Message) (string, error) {
	body := groqChatRequest{
		Model:    p.chatModel,
		Messages: messages,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("chat returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result groqChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

// Vision sends a request to Groq with vision capabilities (e.g., llama-3.2-11b-vision-preview).
// Groq uses base64-encoded images in the data URL format.
func (p *GroqProvider) Vision(ctx context.Context, messages []VisionMessage) (string, error) {
	// Convert VisionMessage to groqVisionMessage format with content as array for multimodal
	groqMessages := make([]groqVisionMessage, len(messages))
	for i, msg := range messages {
		if msg.ImageBase64 != "" {
			// For multimodal messages, content is an array
			content := []groqVisionContent{
				{
					Type: "text",
					Text: msg.Content,
				},
				{
					Type: "image_url",
					ImageURL: struct {
						URL string `json:"url"`
					}{
						URL: fmt.Sprintf("data:%s;base64,%s", msg.MediaType, msg.ImageBase64),
					},
				},
			}
			groqMessages[i] = groqVisionMessage{
				Role:    string(msg.Role),
				Content: content,
			}
		} else {
			// For text-only messages, content is a string
			groqMessages[i] = groqVisionMessage{
				Role:    string(msg.Role),
				Content: msg.Content,
			}
		}
	}

	body := groqVisionRequest{
		Model:    p.chatModel,
		Messages: groqMessages,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal vision request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create vision request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vision returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result groqChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode vision response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}
