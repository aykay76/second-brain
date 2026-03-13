package trending

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/gocolly/colly/v2"
)

const (
	defaultTrendingURL = "https://github.com"
	scraperUserAgent   = "pa-memory-agent/1.0"
)

var velocityRe = regexp.MustCompile(`([\d,]+)\s+stars?\s+(today|this week|this month)`)

type TrendingRepo struct {
	FullName    string
	Owner       string
	Name        string
	Description string
	Language    string
	Stars       int
	StarsToday  int
	HTMLURL     string
}

type Scraper struct {
	baseURL string
}

func NewScraper() *Scraper {
	return &Scraper{baseURL: defaultTrendingURL}
}

// Scrape fetches trending repos for each configured language from GitHub's
// trending page. Repos are deduplicated across language pages.
func (s *Scraper) Scrape(ctx context.Context, languages []string) ([]TrendingRepo, error) {
	seen := make(map[string]bool)
	var allRepos []TrendingRepo

	for _, u := range s.buildURLs(languages) {
		if ctx.Err() != nil {
			return allRepos, ctx.Err()
		}

		repos, err := s.scrapePage(ctx, u)
		if err != nil {
			slog.Warn("failed to scrape trending page", "url", u, "error", err)
			continue
		}

		for _, repo := range repos {
			if !seen[repo.FullName] {
				seen[repo.FullName] = true
				allRepos = append(allRepos, repo)
			}
		}
	}

	if len(allRepos) == 0 {
		return nil, fmt.Errorf("no trending repos scraped")
	}

	return allRepos, nil
}

func (s *Scraper) buildURLs(languages []string) []string {
	urls := make([]string, 0, len(languages)+1)
	urls = append(urls, s.baseURL+"/trending?since=daily")
	for _, lang := range languages {
		slug := strings.ToLower(strings.ReplaceAll(lang, " ", "-"))
		urls = append(urls, fmt.Sprintf("%s/trending/%s?since=daily", s.baseURL, slug))
	}
	return urls
}

func (s *Scraper) scrapePage(ctx context.Context, pageURL string) ([]TrendingRepo, error) {
	var repos []TrendingRepo
	var scrapeErr error

	c := colly.NewCollector(
		colly.UserAgent(scraperUserAgent),
	)

	c.OnRequest(func(r *colly.Request) {
		if ctx.Err() != nil {
			r.Abort()
		}
	})

	c.OnHTML("article.Box-row", func(e *colly.HTMLElement) {
		if repo, ok := parseRepoRow(e); ok {
			repos = append(repos, repo)
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		scrapeErr = fmt.Errorf("scrape %s: status %d: %w", pageURL, r.StatusCode, err)
	})

	if err := c.Visit(pageURL); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("visit %s: %w", pageURL, err)
	}

	if scrapeErr != nil {
		return nil, scrapeErr
	}

	return repos, nil
}

func parseRepoRow(e *colly.HTMLElement) (TrendingRepo, bool) {
	var repo TrendingRepo

	repoLink := strings.TrimSpace(e.ChildAttr("h2 a", "href"))
	repoLink = strings.TrimPrefix(repoLink, "/")
	parts := strings.SplitN(repoLink, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return repo, false
	}

	repo.Owner = strings.TrimSpace(parts[0])
	repo.Name = strings.TrimSpace(parts[1])
	repo.FullName = repo.Owner + "/" + repo.Name
	repo.HTMLURL = "https://github.com/" + repo.FullName

	repo.Description = strings.TrimSpace(e.ChildText("p"))
	repo.Language = strings.TrimSpace(e.ChildText("[itemprop='programmingLanguage']"))

	starText := strings.TrimSpace(e.ChildText("a[href$='/stargazers']"))
	repo.Stars = parseNumber(starText)

	if m := velocityRe.FindStringSubmatch(e.Text); len(m) >= 2 {
		repo.StarsToday = parseNumber(m[1])
	}

	return repo, true
}

func parseNumber(s string) int {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	n, _ := strconv.Atoi(s)
	return n
}
