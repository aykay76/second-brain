package devto

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://dev.to/api"

type Client struct {
	httpClient *http.Client
}

type Article struct {
	ID             int       `json:"id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	BodyHTML       string    `json:"body_html"`
	BodyMarkdown   string    `json:"body_markdown"`
	Published      bool      `json:"published"`
	PublishedAt    time.Time `json:"published_at"`
	URL            string    `json:"url"`
	AuthorID       int       `json:"user_id"`
	Author         *Author   `json:"user"`
	Tags           []string  `json:"tag_list"`
	CoverURL       string    `json:"cover_image_url"`
	ReadingTimeMin int       `json:"reading_time_minutes"`
}

type Author struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	URL      string `json:"profile_image_url"`
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchArticlesByTag fetches articles for a given tag
func (c *Client) FetchArticlesByTag(ctx context.Context, tag string, page, perPage int) ([]Article, error) {
	url := fmt.Sprintf("%s/articles?tag=%s&page=%d&per_page=%d&state=published", baseURL, tag, page, perPage)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch articles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var articles []Article
	if err := json.NewDecoder(resp.Body).Decode(&articles); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return articles, nil
}

// FetchArticleByID fetches a single article by ID with full content
func (c *Client) FetchArticleByID(ctx context.Context, id int) (*Article, error) {
	url := fmt.Sprintf("%s/articles/%d", baseURL, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch article: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var article Article
	if err := json.NewDecoder(resp.Body).Decode(&article); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &article, nil
}
