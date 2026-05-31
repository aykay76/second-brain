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

type OpenAIProvider struct {
	apiKey         string
	baseURL        string
	embeddingModel string
	chatModel      string
	visionModel    string
	dimension      int
	client         *http.Client
}

func NewOpenAIProvider(baseURL, apiKey, embeddingModel, chatModel, visionModel string, dimension int) *OpenAIProvider {
	if visionModel == "" {
		visionModel = chatModel // fall back to chat model if vision model not specified
	}
	return &OpenAIProvider{
		baseURL:        baseURL,
		apiKey:         apiKey,
		embeddingModel: embeddingModel,
		chatModel:      chatModel,
		visionModel:    visionModel,
		dimension:      dimension,
		client: &http.Client{
			Timeout: 600 * time.Second, // 10 minutes for vision models
		},
	}
}

func (p *OpenAIProvider) Dimension() int {
	return p.dimension
}

// --- Embedding ---

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (p *OpenAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body := openAIEmbedRequest{
		Model: p.embeddingModel,
		Input: texts,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	apiURL := p.baseURL
	if apiURL == "" {
		apiURL = "https://api.openai.com/v1"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/embeddings", bytes.NewReader(data))
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

	var result openAIEmbedResponse
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

type openAIChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (p *OpenAIProvider) Complete(ctx context.Context, messages []Message) (string, error) {
	body := openAIChatRequest{
		Model:    p.chatModel,
		Messages: messages,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	apiURL := p.baseURL
	if apiURL == "" {
		apiURL = "https://api.openai.com/v1"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/chat/completions", bytes.NewReader(data))
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

	var result openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

// --- Vision ---

// OpenAI content block types for vision.
type openAIContentText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIContentImage struct {
	Type     string `json:"type"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// openAIVisionMessage uses interface{} for content to support both string and []contentBlock.
type openAIVisionMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type openAIVisionRequest struct {
	Model    string                `json:"model"`
	Messages []openAIVisionMessage `json:"messages"`
}

// Vision sends a request to OpenAI with vision capabilities (e.g., gpt-4o, gpt-4-turbo).
// OpenAI uses base64-encoded images in the data URL format: data:image/jpeg;base64,...
func (p *OpenAIProvider) Vision(ctx context.Context, messages []VisionMessage) (string, error) {
	visionMessages := make([]openAIVisionMessage, len(messages))
	for i, msg := range messages {
		if msg.ImageBase64 != "" {
			// For vision messages, content is an array of content blocks
			content := []interface{}{
				openAIContentText{
					Type: "text",
					Text: msg.Content,
				},
				openAIContentImage{
					Type: "image_url",
					ImageURL: struct {
						URL string `json:"url"`
					}{
						URL: fmt.Sprintf("data:%s;base64,%s", msg.MediaType, msg.ImageBase64),
					},
				},
			}
			visionMessages[i] = openAIVisionMessage{
				Role:    string(msg.Role),
				Content: content,
			}
		} else {
			// For text-only messages, content is a string
			visionMessages[i] = openAIVisionMessage{
				Role:    string(msg.Role),
				Content: msg.Content,
			}
		}
	}

	body := openAIVisionRequest{
		Model:    p.visionModel,
		Messages: visionMessages,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal vision request: %w", err)
	}

	apiURL := p.baseURL
	if apiURL == "" {
		apiURL = "https://api.openai.com/v1"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/chat/completions", bytes.NewReader(data))
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

	var result openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode vision response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}
