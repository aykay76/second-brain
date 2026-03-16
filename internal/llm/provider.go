package llm

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimension() int
}

type ChatProvider interface {
	Complete(ctx context.Context, messages []Message) (string, error)
}

// VisionMessage represents a message with optional image data for vision models.
type VisionMessage struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	// ImageBase64 is the base64-encoded image data (PNG or JPEG).
	// Only used for user messages with images.
	ImageBase64 string `json:"image_base64,omitempty"`
	MediaType   string `json:"media_type,omitempty"` // "image/png" or "image/jpeg"
}

// VisionProvider handles chat completions with vision capabilities.
type VisionProvider interface {
	Vision(ctx context.Context, messages []VisionMessage) (string, error)
}
