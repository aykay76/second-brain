package youtube

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const maxTranscriptLen = 50000

var captionTrackRe = regexp.MustCompile(`"captionTracks":\s*(\[.*?\])`)

// TranscriptFetcher extracts auto-generated or manual captions from YouTube
// videos by parsing the watch page for caption track URLs and then fetching
// the timedtext XML.
type TranscriptFetcher struct {
	httpClient *http.Client
	watchBase  string
}

func NewTranscriptFetcher() *TranscriptFetcher {
	return &TranscriptFetcher{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		watchBase:  "https://www.youtube.com",
	}
}

// Fetch retrieves the transcript text for a video. Returns empty string (no
// error) when captions are unavailable.
func (f *TranscriptFetcher) Fetch(ctx context.Context, videoID string) (string, error) {
	trackURL, err := f.findCaptionTrack(ctx, videoID)
	if err != nil || trackURL == "" {
		return "", err
	}

	return f.fetchTimedText(ctx, trackURL)
}

func (f *TranscriptFetcher) findCaptionTrack(ctx context.Context, videoID string) (string, error) {
	watchURL := f.watchBase + "/watch?v=" + videoID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, watchURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch watch page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", fmt.Errorf("read watch page: %w", err)
	}

	return parseCaptionTrackURL(string(body)), nil
}

type captionTrack struct {
	BaseURL  string `json:"baseUrl"`
	Language string `json:"languageCode"`
	Kind     string `json:"kind"`
}

func parseCaptionTrackURL(pageHTML string) string {
	match := captionTrackRe.FindStringSubmatch(pageHTML)
	if len(match) < 2 {
		return ""
	}

	raw := strings.ReplaceAll(match[1], `\u0026`, "&")
	raw = strings.ReplaceAll(raw, `\"`, `"`)
	raw = strings.ReplaceAll(raw, `\/`, `/`)

	var tracks []captionTrack
	if err := json.Unmarshal([]byte(raw), &tracks); err != nil {
		return ""
	}

	// Prefer manual English captions, fall back to auto-generated, then any.
	var fallback string
	for _, t := range tracks {
		isEnglish := strings.HasPrefix(t.Language, "en")
		if isEnglish && t.Kind != "asr" {
			return t.BaseURL
		}
		if isEnglish && fallback == "" {
			fallback = t.BaseURL
		}
		if fallback == "" {
			fallback = t.BaseURL
		}
	}
	return fallback
}

func (f *TranscriptFetcher) fetchTimedText(ctx context.Context, trackURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trackURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch timedtext: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return "", fmt.Errorf("read timedtext: %w", err)
	}

	return parseTimedText(body), nil
}

type timedTextTranscript struct {
	XMLName xml.Name        `xml:"transcript"`
	Texts   []timedTextNode `xml:"text"`
}

type timedTextNode struct {
	Text string `xml:",chardata"`
}

func parseTimedText(data []byte) string {
	var transcript timedTextTranscript
	if err := xml.Unmarshal(data, &transcript); err != nil {
		return ""
	}

	var b strings.Builder
	for _, t := range transcript.Texts {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
		if b.Len() > maxTranscriptLen {
			break
		}
	}
	return unescapeHTML(b.String())
}

func unescapeHTML(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
	)
	return r.Replace(s)
}
