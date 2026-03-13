package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (c *Client) get(path string, query url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	resp, err := c.httpClient.Get(u)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	return body, nil
}

func (c *Client) post(path string, payload any) ([]byte, error) {
	var reqBody io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	return body, nil
}

// Health checks server connectivity.
func (c *Client) Health() (*HealthResponse, error) {
	data, err := c.get("/health", nil)
	if err != nil {
		return nil, err
	}
	var resp HealthResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Status returns aggregate knowledge base statistics.
func (c *Client) Status() (*StatusResponse, error) {
	data, err := c.get("/status", nil)
	if err != nil {
		return nil, err
	}
	var resp StatusResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Search performs a hybrid or semantic search.
func (c *Client) Search(query string, limit int, mode string) (*SearchResponse, error) {
	q := url.Values{"q": {query}}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if mode != "" {
		q.Set("mode", mode)
	}
	data, err := c.get("/search", q)
	if err != nil {
		return nil, err
	}
	var resp SearchResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Ask sends a question to the RAG pipeline.
func (c *Client) Ask(question string, topK int) (*AskResponse, error) {
	payload := map[string]any{"question": question}
	if topK > 0 {
		payload["top_k"] = topK
	}
	data, err := c.post("/ask", payload)
	if err != nil {
		return nil, err
	}
	var resp AskResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Ingest triggers a syncer. Use "all" to trigger all syncers.
func (c *Client) Ingest(source string) (*IngestResponse, error) {
	data, err := c.post("/ingest/"+source, nil)
	if err != nil {
		return nil, err
	}
	var resp IngestResponse
	return &resp, json.Unmarshal(data, &resp)
}

// ListArtifacts retrieves filtered artifact listings.
func (c *Client) ListArtifacts(source, artifactType string, limit int, sort string) (*ArtifactListResponse, error) {
	q := url.Values{}
	if source != "" {
		q.Set("source", source)
	}
	if artifactType != "" {
		q.Set("type", artifactType)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if sort != "" {
		q.Set("sort", sort)
	}
	data, err := c.get("/artifacts", q)
	if err != nil {
		return nil, err
	}
	var resp ArtifactListResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Related fetches an artifact's graph neighbourhood.
func (c *Client) Related(id string) (*RelatedResponse, error) {
	data, err := c.get("/artifacts/"+id+"/related", nil)
	if err != nil {
		return nil, err
	}
	var resp RelatedResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Tag adds a user tag to an artifact.
func (c *Client) Tag(id, tag string) (*TagResponse, error) {
	data, err := c.post("/artifacts/"+id+"/tags", map[string]string{"tag": tag})
	if err != nil {
		return nil, err
	}
	var resp TagResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Discover triggers the cross-source discovery engine.
func (c *Client) Discover() (*DiscoverResponse, error) {
	data, err := c.post("/discover", nil)
	if err != nil {
		return nil, err
	}
	var resp DiscoverResponse
	return &resp, json.Unmarshal(data, &resp)
}
