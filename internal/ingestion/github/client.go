package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.github.com"
	defaultPerPage = 100
	userAgent      = "pa-memory-agent/1.0"
)

var linkNextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

func NewClient(token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultBaseURL,
		token:      token,
	}
}

func (c *Client) get(ctx context.Context, path string, result any) error {
	url := path
	if !strings.HasPrefix(path, "http") {
		url = c.baseURL + path
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.doWithRateLimit(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api %s: status %d: %s", path, resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

// getPaginated fetches all pages for a paginated GitHub API endpoint.
// Returns accumulated raw JSON arrays decoded into slices of T.
func getPaginated[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	url := c.baseURL + path

	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	url += fmt.Sprintf("%sper_page=%d", sep, defaultPerPage)

	for url != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		c.setHeaders(req)

		resp, err := c.doWithRateLimit(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("github api %s: status %d: %s", url, resp.StatusCode, string(body))
		}

		var page []T
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode page: %w", err)
		}
		resp.Body.Close()

		all = append(all, page...)
		url = extractNextLink(resp.Header.Get("Link"))
	}
	return all, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *Client) doWithRateLimit(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if resetStr := resp.Header.Get("X-RateLimit-Reset"); resetStr != "" {
			if resetUnix, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
				waitUntil := time.Unix(resetUnix, 0)
				wait := time.Until(waitUntil)
				if wait > 0 && wait < 5*time.Minute {
					slog.Warn("github rate limited, waiting", "wait", wait.Round(time.Second))
					resp.Body.Close()
					time.Sleep(wait + time.Second)
					return c.httpClient.Do(req)
				}
			}
		}
	}

	return resp, nil
}

// getPaginatedWithAccept is like getPaginated but overrides the Accept header
// (needed for the starred endpoint which requires a special media type to
// include the starred_at timestamp).
func getPaginatedWithAccept[T any](ctx context.Context, c *Client, path, accept string) ([]T, error) {
	var all []T
	url := c.baseURL + path

	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	url += fmt.Sprintf("%sper_page=%d", sep, defaultPerPage)

	for url != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		c.setHeaders(req)
		req.Header.Set("Accept", accept)

		resp, err := c.doWithRateLimit(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("github api %s: status %d: %s", url, resp.StatusCode, string(body))
		}

		var page []T
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode page: %w", err)
		}
		resp.Body.Close()

		all = append(all, page...)
		url = extractNextLink(resp.Header.Get("Link"))
	}
	return all, nil
}

func extractNextLink(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	matches := linkNextRe.FindStringSubmatch(linkHeader)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}
