package onedrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"pa/internal/config"
)

const (
	defaultAuthority = "https://login.microsoftonline.com"
	graphAPIBase     = "https://graph.microsoft.com/v1.0"
	userAgent        = "pa-memory-agent/1.0"

	deviceCodeGrantType  = "urn:ietf:params:oauth:grant-type:device_code"
	refreshTokenGrant    = "refresh_token"
	graphDefaultScope    = "Files.Read offline_access"

	headerContentType = "Content-Type"
	headerUserAgent   = "User-Agent"
	contentTypeForm   = "application/x-www-form-urlencoded"
)

// tokenStore holds OAuth2 tokens persisted to disk.
type tokenStore struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// DriveItem represents a OneDrive file or folder from the Graph API.
type DriveItem struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Size             int64             `json:"size"`
	WebURL           string            `json:"webUrl"`
	LastModified     time.Time         `json:"-"`
	MimeType         string            `json:"-"`
	ParentPath       string            `json:"-"`
	DownloadURL      string            `json:"-"`
	IsFolder         bool              `json:"-"`
	IsDeleted        bool              `json:"-"`
	File             *driveItemFile    `json:"file"`
	Folder           *driveItemFolder  `json:"folder"`
	Deleted          *json.RawMessage  `json:"deleted"`
	ParentReference  *parentRef        `json:"parentReference"`
	LastModifiedJSON *dateTimeOffset   `json:"lastModifiedDateTime"`
	ContentDownload  *string           `json:"@microsoft.graph.downloadUrl"`
}

type driveItemFile struct {
	MimeType string `json:"mimeType"`
}

type driveItemFolder struct{}

type parentRef struct {
	Path string `json:"path"`
}

type dateTimeOffset struct {
	time.Time
}

func (d *dateTimeOffset) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	d.Time = t
	return nil
}

func (item *DriveItem) normalize() {
	if item.File != nil {
		item.MimeType = item.File.MimeType
	}
	item.IsFolder = item.Folder != nil
	item.IsDeleted = item.Deleted != nil
	if item.ParentReference != nil {
		item.ParentPath = item.ParentReference.Path
	}
	if item.LastModifiedJSON != nil {
		item.LastModified = item.LastModifiedJSON.Time
	}
	if item.ContentDownload != nil {
		item.DownloadURL = *item.ContentDownload
	}
}

// Client wraps the Microsoft Graph API for OneDrive operations.
type Client struct {
	httpClient *http.Client
	graphBase  string
	authBase   string
	cfg        config.OneDriveConfig
	mu         sync.Mutex
	tokens     *tokenStore
}

func NewClient(cfg config.OneDriveConfig) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		graphBase:  graphAPIBase,
		authBase:   defaultAuthority,
		cfg:        cfg,
	}
}

// Authenticate performs the device code flow or loads existing tokens.
// If valid tokens exist on disk they are reused; expired tokens are
// refreshed automatically.
func (c *Client) Authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.loadTokens(); err == nil && c.tokens != nil {
		if time.Now().Before(c.tokens.ExpiresAt.Add(-1 * time.Minute)) {
			return nil
		}
		if c.tokens.RefreshToken != "" {
			if err := c.refreshAccessToken(ctx); err == nil {
				return nil
			}
			slog.Warn("token refresh failed, starting device code flow")
		}
	}

	return c.deviceCodeFlow(ctx)
}

// DeviceCodeResponse is returned by the /devicecode endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

func (c *Client) deviceCodeFlow(ctx context.Context) error {
	tenantID := c.cfg.TenantID
	if tenantID == "" {
		tenantID = "consumers"
	}

	codeResp, err := c.requestDeviceCode(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("request device code: %w", err)
	}

	slog.Info("OneDrive authentication required",
		"message", codeResp.Message,
		"url", codeResp.VerificationURI,
		"code", codeResp.UserCode,
	)
	fmt.Printf("\n=== OneDrive Authentication ===\n%s\n\n", codeResp.Message)

	interval := time.Duration(codeResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(codeResp.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}

		tokens, err := c.pollForToken(ctx, tenantID, codeResp.DeviceCode)
		if err != nil {
			if strings.Contains(err.Error(), "authorization_pending") {
				continue
			}
			if strings.Contains(err.Error(), "slow_down") {
				interval += 5 * time.Second
				continue
			}
			return fmt.Errorf("poll for token: %w", err)
		}

		c.tokens = tokens
		if err := c.saveTokens(); err != nil {
			slog.Warn("failed to save tokens", "error", err)
		}
		slog.Info("OneDrive authentication successful")
		return nil
	}

	return fmt.Errorf("device code flow timed out")
}

func (c *Client) requestDeviceCode(ctx context.Context, tenantID string) (*DeviceCodeResponse, error) {
	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/devicecode", c.authBase, tenantID)

	data := url.Values{
		"client_id": {c.cfg.ClientID},
		"scope":     {graphDefaultScope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set(headerContentType, contentTypeForm)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, body)
	}

	var codeResp DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&codeResp); err != nil {
		return nil, err
	}
	return &codeResp, nil
}

func (c *Client) pollForToken(ctx context.Context, tenantID, deviceCode string) (*tokenStore, error) {
	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/token", c.authBase, tenantID)

	data := url.Values{
		"grant_type":  {deviceCodeGrantType},
		"client_id":   {c.cfg.ClientID},
		"device_code": {deviceCode},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set(headerContentType, contentTypeForm)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		json.Unmarshal(body, &errResp)
		return nil, fmt.Errorf("%s", errResp.Error)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	return &tokenStore{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}, nil
}

func (c *Client) refreshAccessToken(ctx context.Context) error {
	tenantID := c.cfg.TenantID
	if tenantID == "" {
		tenantID = "consumers"
	}

	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/token", c.authBase, tenantID)

	data := url.Values{
		"grant_type":    {refreshTokenGrant},
		"client_id":     {c.cfg.ClientID},
		"refresh_token": {c.tokens.RefreshToken},
		"scope":         {graphDefaultScope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set(headerContentType, contentTypeForm)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return err
	}

	c.tokens.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		c.tokens.RefreshToken = tokenResp.RefreshToken
	}
	c.tokens.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return c.saveTokens()
}

func (c *Client) loadTokens() error {
	path := c.cfg.TokenFile
	if path == "" {
		path = "config/onedrive_tokens.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var ts tokenStore
	if err := json.Unmarshal(data, &ts); err != nil {
		return err
	}
	c.tokens = &ts
	return nil
}

func (c *Client) saveTokens() error {
	path := c.cfg.TokenFile
	if path == "" {
		path = "config/onedrive_tokens.json"
	}

	data, err := json.MarshalIndent(c.tokens, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (c *Client) ensureAuth(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tokens == nil {
		return fmt.Errorf("not authenticated — run authentication first")
	}

	if time.Now().After(c.tokens.ExpiresAt.Add(-1 * time.Minute)) {
		if c.tokens.RefreshToken != "" {
			return c.refreshAccessToken(ctx)
		}
		return fmt.Errorf("token expired and no refresh token available")
	}

	return nil
}

// DeltaResponse represents a page of delta query results.
type DeltaResponse struct {
	Items     []DriveItem `json:"value"`
	NextLink  string      `json:"@odata.nextLink"`
	DeltaLink string      `json:"@odata.deltaLink"`
}

// ListFolder retrieves all items in a OneDrive folder path (e.g. "/Documents/Engineering").
func (c *Client) ListFolder(ctx context.Context, folderPath string) ([]DriveItem, error) {
	if err := c.ensureAuth(ctx); err != nil {
		return nil, err
	}

	encoded := url.PathEscape(strings.TrimPrefix(folderPath, "/"))
	endpoint := fmt.Sprintf("%s/me/drive/root:/%s:/children?$top=200", c.graphBase, encoded)

	var allItems []DriveItem
	for endpoint != "" {
		var resp struct {
			Value    []DriveItem `json:"value"`
			NextLink string      `json:"@odata.nextLink"`
		}
		if err := c.graphGet(ctx, endpoint, &resp); err != nil {
			return nil, fmt.Errorf("list folder %q: %w", folderPath, err)
		}
		for i := range resp.Value {
			resp.Value[i].normalize()
		}
		allItems = append(allItems, resp.Value...)
		endpoint = resp.NextLink
	}

	return allItems, nil
}

// DeltaQuery performs an incremental sync. If deltaLink is empty, a full
// initial sync is performed for the given folder path.
func (c *Client) DeltaQuery(ctx context.Context, folderPath, deltaLink string) (*DeltaResponse, error) {
	if err := c.ensureAuth(ctx); err != nil {
		return nil, err
	}

	endpoint := deltaLink
	if endpoint == "" {
		encoded := url.PathEscape(strings.TrimPrefix(folderPath, "/"))
		endpoint = fmt.Sprintf("%s/me/drive/root:/%s:/delta?$top=200", c.graphBase, encoded)
	}

	result := &DeltaResponse{}
	for endpoint != "" {
		var page DeltaResponse
		if err := c.graphGet(ctx, endpoint, &page); err != nil {
			return nil, fmt.Errorf("delta query: %w", err)
		}
		for i := range page.Items {
			page.Items[i].normalize()
		}
		result.Items = append(result.Items, page.Items...)
		result.DeltaLink = page.DeltaLink
		endpoint = page.NextLink
	}

	return result, nil
}

// DownloadContent fetches the file content from the given download URL.
func (c *Client) DownloadContent(ctx context.Context, downloadURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(headerUserAgent, userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// DownloadItem fetches content for a DriveItem by its ID using the Graph API.
func (c *Client) DownloadItem(ctx context.Context, itemID string) ([]byte, error) {
	if err := c.ensureAuth(ctx); err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/me/drive/items/%s/content", c.graphBase, itemID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.tokens.AccessToken)
	req.Header.Set(headerUserAgent, userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download item: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download item %d: %s", resp.StatusCode, body)
	}

	return io.ReadAll(resp.Body)
}

func (c *Client) graphGet(ctx context.Context, endpoint string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.tokens.AccessToken)
	req.Header.Set(headerUserAgent, userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("graph request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("graph api %d: %s", resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(result)
}
