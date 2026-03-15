package thenewstack

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

const (
	baseURL        = "https://thenewstack.io"
	scraperTimeout = 30 * time.Second
	scraperAgent   = "pa-memory-agent/1.0"
)

type Article struct {
	Title       string
	URL         string
	Description string
	PublishedAt time.Time
	Content     string
	Categories  []string
	Authors     []string
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: scraperTimeout,
		},
	}
}

// ScrapeLatestArticles fetches the latest articles from The New Stack homepage
func (c *Client) ScrapeLatestArticles(ctx context.Context, limit int) ([]Article, error) {
	var articles []Article

	collector := colly.NewCollector(
		colly.UserAgent(scraperAgent),
	)

	collector.OnRequest(func(r *colly.Request) {
		if ctx.Err() != nil {
			r.Abort()
		}
	})

	// Parse article listing page
	collector.OnHTML("article, .post-item, [data-article]", func(e *colly.HTMLElement) {
		if len(articles) >= limit {
			return
		}

		article := c.parseArticleElement(e)
		if article.URL != "" {
			articles = append(articles, article)
		}
	})

	collector.OnError(func(_ *colly.Response, err error) {
		slog.Warn("collector error while scraping thenewstack", "error", err)
	})

	err := collector.Visit(c.baseURL + "/news")
	if err != nil {
		return articles, fmt.Errorf("scrape thenewstack: %w", err)
	}

	slog.Info("scraped thenewstack articles", "count", len(articles))
	return articles, nil
}

// parseArticleElement extracts article metadata from a DOM element
func (c *Client) parseArticleElement(e *colly.HTMLElement) Article {
	article := Article{}

	// Try various selectors for title
	article.Title = strings.TrimSpace(e.ChildAttr("h2 a, h3 a, .article-title a, a[itemprop='headline']", "title"))
	if article.Title == "" {
		article.Title = strings.TrimSpace(e.ChildText("h2 a, h3 a, .article-title a, a[itemprop='headline']"))
	}

	// Extract URL
	article.URL = e.ChildAttr("h2 a, h3 a, .article-title a, a[itemprop='headline']", "href")
	article.URL = c.resolveURL(article.URL)

	// Extract description/excerpt
	article.Description = strings.TrimSpace(e.ChildText("p, .excerpt, [itemprop='description']"))

	// Extract author(s)
	e.ForEach(".author, [itemprop='author'], .by-author a", func(_ int, el *colly.HTMLElement) {
		author := strings.TrimSpace(el.Text)
		if author != "" {
			article.Authors = append(article.Authors, author)
		}
	})

	// Extract categories/tags
	e.ForEach("a[rel='tag'], .category-tag, .article-category", func(_ int, el *colly.HTMLElement) {
		category := strings.TrimSpace(el.Text)
		if category != "" {
			article.Categories = append(article.Categories, category)
		}
	})

	// Try to parse published date
	dateStr := e.ChildAttr("time, [itemprop='datePublished']", "datetime")
	if dateStr != "" {
		if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
			article.PublishedAt = t
		} else if t, err := time.Parse("2006-01-02", dateStr); err == nil {
			article.PublishedAt = t
		}
	}

	return article
}

// FetchArticleContent retrieves the full article content
func (c *Client) FetchArticleContent(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", scraperAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch article: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch article: status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	// Basic content extraction - look for article content div
	content := string(bodyBytes)
	if idx := strings.Index(content, `<main`); idx != -1 {
		content = content[idx:]
		if endIdx := strings.Index(content, `</main>`); endIdx != -1 {
			content = content[:endIdx]
		}
	}

	return content, nil
}

// resolveURL converts relative URLs to absolute
func (c *Client) resolveURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		return rawURL
	}
	if strings.HasPrefix(rawURL, "/") {
		return c.baseURL + rawURL
	}
	return c.baseURL + "/" + rawURL
}
