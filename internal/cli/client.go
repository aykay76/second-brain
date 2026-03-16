package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// IngestVision starts an async vision ingestion job.
func (c *Client) IngestVision() (*VisionJobResponse, error) {
	data, err := c.post("/ingest/vision", nil)
	if err != nil {
		return nil, err
	}
	var resp VisionJobResponse
	return &resp, json.Unmarshal(data, &resp)
}

// VisionJobStatus gets the status of a vision ingestion job.
func (c *Client) VisionJobStatus(jobID string) (*VisionJobStatus, error) {
	data, err := c.get("/ingest/vision/jobs/"+jobID, nil)
	if err != nil {
		return nil, err
	}
	var resp VisionJobStatus
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

// Enrich triggers auto-tagging and summary generation for pending artifacts.
func (c *Client) Enrich() (*EnrichResponse, error) {
	data, err := c.post("/enrich", nil)
	if err != nil {
		return nil, err
	}
	var resp EnrichResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Digest generates a knowledge base digest for a time period.
func (c *Client) Digest(period, from, to, natural string) (*DigestResponse, error) {
	q := url.Values{}
	if period != "" {
		q.Set("period", period)
	}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	if natural != "" {
		q.Set("natural", natural)
	}
	data, err := c.get("/digest", q)
	if err != nil {
		return nil, err
	}
	var resp DigestResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Gems retrieves forgotten gems (older artifacts similar to recent activity).
func (c *Client) Gems(lookbackDays int) (*GemsResponse, error) {
	q := url.Values{}
	if lookbackDays > 0 {
		q.Set("lookback", fmt.Sprintf("%d", lookbackDays))
	}
	data, err := c.get("/insights/gems", q)
	if err != nil {
		return nil, err
	}
	var resp GemsResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Serendipity retrieves surprising cross-source connections.
func (c *Client) Serendipity(period, natural string) (*SerendipityResponse, error) {
	q := url.Values{}
	if period != "" {
		q.Set("period", period)
	}
	if natural != "" {
		q.Set("natural", natural)
	}
	data, err := c.get("/insights/serendipity", q)
	if err != nil {
		return nil, err
	}
	var resp SerendipityResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Topics retrieves topic momentum and drift analysis.
func (c *Client) Topics(weeks int) (*TopicsResponse, error) {
	q := url.Values{}
	if weeks > 0 {
		q.Set("weeks", fmt.Sprintf("%d", weeks))
	}
	data, err := c.get("/insights/topics", q)
	if err != nil {
		return nil, err
	}
	var resp TopicsResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Depth retrieves knowledge depth analysis.
func (c *Client) Depth() (*DepthResponse, error) {
	data, err := c.get("/insights/depth", nil)
	if err != nil {
		return nil, err
	}
	var resp DepthResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Velocity retrieves learning velocity metrics.
func (c *Client) Velocity(period, natural string) (*VelocityResponse, error) {
	q := url.Values{}
	if period != "" {
		q.Set("period", period)
	}
	if natural != "" {
		q.Set("natural", natural)
	}
	data, err := c.get("/insights/velocity", q)
	if err != nil {
		return nil, err
	}
	var resp VelocityResponse
	return &resp, json.Unmarshal(data, &resp)
}

// Memories retrieves "this time last month/year" memories.
func (c *Client) Memories() (*MemoriesResponse, error) {
	data, err := c.get("/insights/memories", nil)
	if err != nil {
		return nil, err
	}
	var resp MemoriesResponse
	return &resp, json.Unmarshal(data, &resp)
}

// SearchWithTags performs a hybrid or semantic search, optionally filtered by tags.
func (c *Client) SearchWithTags(query string, limit int, mode string, tags []string) (*SearchResponse, error) {
	q := url.Values{"q": {query}}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if mode != "" {
		q.Set("mode", mode)
	}
	if len(tags) > 0 {
		q.Set("tags", strings.Join(tags, ","))
	}
	data, err := c.get("/search", q)
	if err != nil {
		return nil, err
	}
	var resp SearchResponse
	return &resp, json.Unmarshal(data, &resp)
}
