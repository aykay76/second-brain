package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAPIBase    = "https://www.googleapis.com/youtube/v3"
	defaultMaxResults = 50
	userAgent         = "pa-memory-agent/1.0"
)

// Video holds the parsed representation of a YouTube video.
type Video struct {
	VideoID     string
	Title       string
	Description string
	ChannelID   string
	Channel     string
	PublishedAt time.Time
	Tags        []string
	Duration    string
	ViewCount   int64
	LikeCount   int64
	Thumbnail   string
}

// Client interacts with the YouTube Data API v3.
type Client struct {
	httpClient *http.Client
	apiBase    string
	apiKey     string
}

func NewClient(apiKey string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBase:    defaultAPIBase,
		apiKey:     apiKey,
	}
}

// SearchByChannel returns videos from a specific channel, optionally filtered
// by publish date for incremental sync.
func (c *Client) SearchByChannel(ctx context.Context, channelID string, publishedAfter time.Time, maxResults int) ([]Video, error) {
	params := url.Values{
		"part":       {"snippet"},
		"channelId":  {channelID},
		"type":       {"video"},
		"order":      {"date"},
		"maxResults": {fmt.Sprintf("%d", clamp(maxResults, 1, 50))},
		"key":        {c.apiKey},
	}
	if !publishedAfter.IsZero() {
		params.Set("publishedAfter", publishedAfter.Format(time.RFC3339))
	}

	return c.searchVideos(ctx, params)
}

// SearchByQuery discovers videos matching a search term.
func (c *Client) SearchByQuery(ctx context.Context, query string, publishedAfter time.Time, maxResults int) ([]Video, error) {
	params := url.Values{
		"part":       {"snippet"},
		"q":          {query},
		"type":       {"video"},
		"order":      {"relevance"},
		"maxResults": {fmt.Sprintf("%d", clamp(maxResults, 1, 50))},
		"key":        {c.apiKey},
	}
	if !publishedAfter.IsZero() {
		params.Set("publishedAfter", publishedAfter.Format(time.RFC3339))
	}

	return c.searchVideos(ctx, params)
}

// EnrichVideos fetches full details (contentDetails, statistics) for a batch
// of video IDs and merges them into the provided slice.
func (c *Client) EnrichVideos(ctx context.Context, videos []Video) error {
	if len(videos) == 0 {
		return nil
	}

	ids := make([]string, len(videos))
	for i, v := range videos {
		ids[i] = v.VideoID
	}

	for i := 0; i < len(ids); i += 50 {
		end := i + 50
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[i:end]

		params := url.Values{
			"part": {"snippet,contentDetails,statistics"},
			"id":   {strings.Join(batch, ",")},
			"key":  {c.apiKey},
		}

		var resp videoListResponse
		if err := c.apiGet(ctx, "/videos", params, &resp); err != nil {
			return fmt.Errorf("enrich videos: %w", err)
		}

		details := make(map[string]videoItem, len(resp.Items))
		for _, item := range resp.Items {
			details[item.ID] = item
		}

		for j := range videos {
			if d, ok := details[videos[j].VideoID]; ok {
				mergeDetails(&videos[j], d)
			}
		}
	}

	return nil
}

func (c *Client) searchVideos(ctx context.Context, params url.Values) ([]Video, error) {
	var resp searchResponse
	if err := c.apiGet(ctx, "/search", params, &resp); err != nil {
		return nil, err
	}

	videos := make([]Video, 0, len(resp.Items))
	for _, item := range resp.Items {
		if item.ID.VideoID == "" {
			continue
		}
		pub, _ := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
		videos = append(videos, Video{
			VideoID:     item.ID.VideoID,
			Title:       item.Snippet.Title,
			Description: item.Snippet.Description,
			ChannelID:   item.Snippet.ChannelID,
			Channel:     item.Snippet.ChannelTitle,
			PublishedAt: pub,
			Thumbnail:   bestThumbnail(item.Snippet.Thumbnails),
		})
	}

	return videos, nil
}

func (c *Client) apiGet(ctx context.Context, path string, params url.Values, result any) error {
	reqURL := c.apiBase + path + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("youtube api status %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

// --- YouTube API response types ---

type searchResponse struct {
	Items         []searchItem `json:"items"`
	NextPageToken string       `json:"nextPageToken"`
}

type searchItem struct {
	ID      searchItemID `json:"id"`
	Snippet snippet      `json:"snippet"`
}

type searchItemID struct {
	VideoID string `json:"videoId"`
}

type snippet struct {
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	ChannelID    string       `json:"channelId"`
	ChannelTitle string       `json:"channelTitle"`
	PublishedAt  string       `json:"publishedAt"`
	Tags         []string     `json:"tags"`
	Thumbnails   thumbnailSet `json:"thumbnails"`
}

type thumbnailSet struct {
	Default  *thumbnail `json:"default"`
	Medium   *thumbnail `json:"medium"`
	High     *thumbnail `json:"high"`
	Standard *thumbnail `json:"standard"`
	MaxRes   *thumbnail `json:"maxres"`
}

type thumbnail struct {
	URL string `json:"url"`
}

type videoListResponse struct {
	Items []videoItem `json:"items"`
}

type videoItem struct {
	ID             string         `json:"id"`
	Snippet        snippet        `json:"snippet"`
	ContentDetails contentDetails `json:"contentDetails"`
	Statistics     statistics     `json:"statistics"`
}

type contentDetails struct {
	Duration string `json:"duration"`
}

type statistics struct {
	ViewCount  stringInt `json:"viewCount"`
	LikeCount  stringInt `json:"likeCount"`
}

// stringInt handles YouTube's numeric fields that are JSON strings.
type stringInt int64

func (s *stringInt) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		var n int64
		if err2 := json.Unmarshal(b, &n); err2 != nil {
			return err
		}
		*s = stringInt(n)
		return nil
	}
	var n int64
	for _, c := range str {
		if c >= '0' && c <= '9' {
			n = n*10 + int64(c-'0')
		}
	}
	*s = stringInt(n)
	return nil
}

func mergeDetails(v *Video, d videoItem) {
	if d.Snippet.Tags != nil {
		v.Tags = d.Snippet.Tags
	}
	v.Duration = d.ContentDetails.Duration
	v.ViewCount = int64(d.Statistics.ViewCount)
	v.LikeCount = int64(d.Statistics.LikeCount)
}

func bestThumbnail(t thumbnailSet) string {
	switch {
	case t.MaxRes != nil:
		return t.MaxRes.URL
	case t.Standard != nil:
		return t.Standard.URL
	case t.High != nil:
		return t.High.URL
	case t.Medium != nil:
		return t.Medium.URL
	case t.Default != nil:
		return t.Default.URL
	default:
		return ""
	}
}

func clamp(n, min, max int) int {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
