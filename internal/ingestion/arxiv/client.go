package arxiv

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL    = "https://export.arxiv.org/api/query"
	defaultMaxResults = 200
	userAgent         = "pa-memory-agent/1.0"
	requestDelay      = 3 * time.Second
)

// Atom feed structures matching the arXiv API response.
type atomFeed struct {
	XMLName      xml.Name    `xml:"feed"`
	TotalResults int         `xml:"totalResults"`
	StartIndex   int         `xml:"startIndex"`
	ItemsPerPage int         `xml:"itemsPerPage"`
	Entries      []atomEntry `xml:"entry"`
}

type atomEntry struct {
	ID         string       `xml:"id"`
	Title      string       `xml:"title"`
	Summary    string       `xml:"summary"`
	Published  string       `xml:"published"`
	Updated    string       `xml:"updated"`
	Authors    []atomAuthor `xml:"author"`
	Links      []atomLink   `xml:"link"`
	Categories []atomCat    `xml:"category"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomLink struct {
	Href  string `xml:"href,attr"`
	Rel   string `xml:"rel,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}

type atomCat struct {
	Term string `xml:"term,attr"`
}

// Paper is the parsed representation of an arXiv paper.
type Paper struct {
	ArXivID       string
	Title         string
	Abstract      string
	Authors       []string
	Categories    []string
	Published     time.Time
	Updated       time.Time
	AbstractURL   string
	PDFURL        string
}

type Client struct {
	httpClient *http.Client
	baseURL    string
	lastReq    time.Time
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    defaultBaseURL,
	}
}

// Search queries the arXiv API and returns parsed papers.
// It builds the query from categories and keywords, filtering by date range.
func (c *Client) Search(ctx context.Context, categories, keywords []string, from, to time.Time, maxResults int) ([]Paper, error) {
	query := buildSearchQuery(categories, keywords, from, to)
	if query == "" {
		return nil, fmt.Errorf("empty search query: provide at least one category or keyword")
	}

	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

	var allPapers []Paper
	start := 0

	for {
		c.rateLimit()

		reqURL := fmt.Sprintf("%s?search_query=%s&start=%d&max_results=%d&sortBy=submittedDate&sortOrder=descending",
			c.baseURL, url.QueryEscape(query), start, maxResults)

		feed, err := c.fetchFeed(ctx, reqURL)
		if err != nil {
			return allPapers, fmt.Errorf("fetch arxiv feed: %w", err)
		}

		papers := parseFeed(feed)
		allPapers = append(allPapers, papers...)

		slog.Info("arxiv batch fetched",
			"start", start,
			"received", len(papers),
			"total_results", feed.TotalResults,
		)

		if len(papers) == 0 || start+len(papers) >= feed.TotalResults || start+len(papers) >= maxResults {
			break
		}
		start += len(papers)
	}

	return allPapers, nil
}

func (c *Client) fetchFeed(ctx context.Context, reqURL string) (*atomFeed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("arxiv api status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse atom feed: %w", err)
	}

	return &feed, nil
}

func (c *Client) rateLimit() {
	elapsed := time.Since(c.lastReq)
	if elapsed < requestDelay {
		time.Sleep(requestDelay - elapsed)
	}
	c.lastReq = time.Now()
}

func buildSearchQuery(categories, keywords []string, from, to time.Time) string {
	var parts []string

	if len(categories) > 0 {
		var catParts []string
		for _, cat := range categories {
			catParts = append(catParts, "cat:"+cat)
		}
		parts = append(parts, "("+strings.Join(catParts, "+OR+")+")")
	}

	if len(keywords) > 0 {
		var kwParts []string
		for _, kw := range keywords {
			escaped := strings.ReplaceAll(kw, " ", "+")
			kwParts = append(kwParts, fmt.Sprintf("all:%q", escaped))
		}
		parts = append(parts, "("+strings.Join(kwParts, "+OR+")+")")
	}

	if !from.IsZero() || !to.IsZero() {
		fromStr := "000000000000"
		toStr := "999912312359"
		if !from.IsZero() {
			fromStr = from.Format("200601021504")
		}
		if !to.IsZero() {
			toStr = to.Format("200601021504")
		}
		parts = append(parts, fmt.Sprintf("submittedDate:[%s+TO+%s]", fromStr, toStr))
	}

	return strings.Join(parts, "+AND+")
}

func parseFeed(feed *atomFeed) []Paper {
	papers := make([]Paper, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		if p, ok := parseEntry(e); ok {
			papers = append(papers, p)
		}
	}
	return papers
}

func parseEntry(e atomEntry) (Paper, bool) {
	p := Paper{
		ArXivID:  extractArXivID(e.ID),
		Title:    normaliseWhitespace(e.Title),
		Abstract: normaliseWhitespace(e.Summary),
	}
	if p.ArXivID == "" {
		return p, false
	}

	for _, a := range e.Authors {
		p.Authors = append(p.Authors, strings.TrimSpace(a.Name))
	}
	for _, c := range e.Categories {
		p.Categories = append(p.Categories, c.Term)
	}

	p.Published, _ = time.Parse(time.RFC3339, e.Published)
	p.Updated, _ = time.Parse(time.RFC3339, e.Updated)

	for _, link := range e.Links {
		if link.Title == "pdf" {
			p.PDFURL = link.Href
		} else if link.Rel == "alternate" {
			p.AbstractURL = link.Href
		}
	}

	if p.AbstractURL == "" {
		p.AbstractURL = "https://arxiv.org/abs/" + p.ArXivID
	}
	if p.PDFURL == "" {
		p.PDFURL = "https://arxiv.org/pdf/" + p.ArXivID
	}

	return p, true
}

// extractArXivID extracts the bare arXiv ID from a full URL like
// "http://arxiv.org/abs/2403.12345v1".
func extractArXivID(rawID string) string {
	rawID = strings.TrimSpace(rawID)
	if idx := strings.Index(rawID, "/abs/"); idx >= 0 {
		return rawID[idx+5:]
	}
	return rawID
}

func normaliseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
