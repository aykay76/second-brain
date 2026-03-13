package github

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"pa/internal/config"
	"pa/internal/ingestion"
	"pa/internal/retrieval"
)

const reposPrefix = "/repos/"

var issueRefRe = regexp.MustCompile(`(?:([a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+))?#(\d+)`)

type Syncer struct {
	db       *sql.DB
	embedSvc *retrieval.EmbeddingService
	client   *Client
	cfg      config.GitHubConfig
}

func NewSyncer(db *sql.DB, embedSvc *retrieval.EmbeddingService, cfg config.GitHubConfig) *Syncer {
	return &Syncer{
		db:       db,
		embedSvc: embedSvc,
		client:   NewClient(cfg.Token),
		cfg:      cfg,
	}
}

func (s *Syncer) Name() string { return "github" }

func (s *Syncer) Sync(ctx context.Context) (*ingestion.SyncResult, error) {
	result := &ingestion.SyncResult{}

	repos, err := s.fetchOwnedRepos(ctx)
	if err != nil {
		return result, fmt.Errorf("fetch owned repos: %w", err)
	}

	for _, repo := range s.cfg.IncludeRepos {
		r, err := s.fetchRepo(ctx, repo)
		if err != nil {
			slog.Warn("failed to fetch included repo", "repo", repo, "error", err)
			result.Errors++
			continue
		}
		repos = append(repos, *r)
	}

	for i := range repos {
		s.syncRepo(ctx, &repos[i], result)
	}

	if s.cfg.SyncStarred {
		s.syncStarred(ctx, result)
	}

	if s.cfg.SyncGists {
		s.syncGists(ctx, result)
	}

	slog.Info("github sync complete",
		"ingested", result.Ingested,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result, nil
}

// --- API types ---

type ghRepo struct {
	FullName      string   `json:"full_name"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Language      string   `json:"language"`
	Topics        []string `json:"topics"`
	HTMLURL       string   `json:"html_url"`
	DefaultBranch string   `json:"default_branch"`
	StarCount     int      `json:"stargazers_count"`
	ForksCount    int      `json:"forks_count"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	PushedAt  time.Time `json:"pushed_at"`
	Fork      bool      `json:"fork"`
}

type ghReadme struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type ghPR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	Merged    bool      `json:"merged"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

type ghComment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

type ghCommit struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name  string    `json:"name"`
			Email string    `json:"email"`
			Date  time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

type ghCommitDetail struct {
	Stats struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Total     int `json:"total"`
	} `json:"stats"`
	Files []struct {
		Filename  string `json:"filename"`
		Status    string `json:"status"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	} `json:"files"`
}

type ghStarredRepo struct {
	StarredAt string `json:"starred_at"`
	Repo      ghRepo `json:"repo"`
}

type ghGist struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	HTMLURL     string    `json:"html_url"`
	Public      bool      `json:"public"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Files       map[string]struct {
		Filename string `json:"filename"`
		Language string `json:"language"`
		RawURL   string `json:"raw_url"`
		Size     int    `json:"size"`
		Content  string `json:"content"`
	} `json:"files"`
}

// --- Fetch methods ---

func (s *Syncer) fetchOwnedRepos(ctx context.Context) ([]ghRepo, error) {
	return getPaginated[ghRepo](ctx, s.client, "/user/repos?affiliation=owner&sort=pushed&direction=desc")
}

func (s *Syncer) fetchRepo(ctx context.Context, fullName string) (*ghRepo, error) {
	var repo ghRepo
	if err := s.client.get(ctx, "/repos/"+fullName, &repo); err != nil {
		return nil, err
	}
	return &repo, nil
}

func (s *Syncer) fetchReadme(ctx context.Context, fullName string) (string, error) {
	var readme ghReadme
	if err := s.client.get(ctx, reposPrefix+fullName+"/readme", &readme); err != nil {
		return "", err
	}
	if readme.Encoding == "base64" {
		cleaned := strings.ReplaceAll(readme.Content, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(cleaned)
		if err != nil {
			return "", fmt.Errorf("decode readme: %w", err)
		}
		return string(decoded), nil
	}
	return readme.Content, nil
}

func (s *Syncer) fetchPRs(ctx context.Context, fullName string) ([]ghPR, error) {
	return getPaginated[ghPR](ctx, s.client, reposPrefix+fullName+"/pulls?state=all&sort=updated&direction=desc")
}

func (s *Syncer) fetchPRComments(ctx context.Context, fullName string, number int) ([]ghComment, error) {
	path := fmt.Sprintf("%s%s/issues/%d/comments", reposPrefix, fullName, number)
	return getPaginated[ghComment](ctx, s.client, path)
}

func (s *Syncer) fetchCommits(ctx context.Context, fullName string) ([]ghCommit, error) {
	return getPaginated[ghCommit](ctx, s.client, reposPrefix+fullName+"/commits")
}

func (s *Syncer) fetchCommitDetail(ctx context.Context, fullName, sha string) (*ghCommitDetail, error) {
	var detail ghCommitDetail
	if err := s.client.get(ctx, fmt.Sprintf("%s%s/commits/%s", reposPrefix, fullName, sha), &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

func (s *Syncer) fetchStarred(ctx context.Context) ([]ghStarredRepo, error) {
	return getPaginatedWithAccept[ghStarredRepo](ctx, s.client, "/user/starred?sort=created&direction=desc", "application/vnd.github.star+json")
}

func (s *Syncer) fetchGists(ctx context.Context) ([]ghGist, error) {
	return getPaginated[ghGist](ctx, s.client, "/gists")
}

func (s *Syncer) fetchGistDetail(ctx context.Context, id string) (*ghGist, error) {
	var gist ghGist
	if err := s.client.get(ctx, "/gists/"+id, &gist); err != nil {
		return nil, err
	}
	return &gist, nil
}

// --- Sync methods ---

func (s *Syncer) syncRepo(ctx context.Context, repo *ghRepo, result *ingestion.SyncResult) {
	cursor := s.getCursor(ctx, "github:repo:"+repo.FullName)

	if !cursor.IsZero() && !repo.PushedAt.After(cursor) {
		slog.Debug("repo unchanged since last sync", "repo", repo.FullName)
		result.Skipped++
	} else {
		readme, err := s.fetchReadme(ctx, repo.FullName)
		if err != nil {
			slog.Debug("no readme for repo", "repo", repo.FullName, "error", err)
		}

		content := repo.Description
		if readme != "" {
			content += "\n\n" + readme
		}

		metadata := map[string]any{
			"owner":          repo.Owner.Login,
			"language":       repo.Language,
			"topics":         repo.Topics,
			"stars":          repo.StarCount,
			"forks":          repo.ForksCount,
			"default_branch": repo.DefaultBranch,
			"fork":           repo.Fork,
		}

		if err := s.upsertAndEmbed(ctx, artifactInput{
			ArtifactType: "repo", ExternalID: "repo:" + repo.FullName,
			Title: repo.Name, Content: content, Metadata: metadata,
			SourceURL: repo.HTMLURL, CreatedAt: repo.CreatedAt,
		}); err != nil {
			slog.Error("failed to sync repo", "repo", repo.FullName, "error", err)
			result.Errors++
		} else {
			result.Ingested++
		}
	}

	s.syncPRs(ctx, repo.FullName, result)
	s.syncCommits(ctx, repo.FullName, result)
	s.setCursor(ctx, "github:repo:"+repo.FullName, time.Now())
}

func (s *Syncer) syncPRs(ctx context.Context, fullName string, result *ingestion.SyncResult) {
	cursor := s.getCursor(ctx, "github:prs:"+fullName)

	prs, err := s.fetchPRs(ctx, fullName)
	if err != nil {
		slog.Error("failed to fetch PRs", "repo", fullName, "error", err)
		result.Errors++
		return
	}

	for _, pr := range prs {
		if !cursor.IsZero() && !pr.UpdatedAt.After(cursor) {
			result.Skipped++
			continue
		}

		content := pr.Body
		comments, err := s.fetchPRComments(ctx, fullName, pr.Number)
		if err != nil {
			slog.Warn("failed to fetch PR comments", "repo", fullName, "pr", pr.Number, "error", err)
		} else if len(comments) > 0 {
			var commentParts []string
			for _, c := range comments {
				commentParts = append(commentParts, fmt.Sprintf("@%s:\n%s", c.User.Login, c.Body))
			}
			content += "\n\n--- Comments ---\n\n" + strings.Join(commentParts, "\n\n---\n\n")
		}

		externalID := fmt.Sprintf("pr:%s#%d", fullName, pr.Number)
		metadata := map[string]any{
			"repo":       fullName,
			"number":     pr.Number,
			"state":      pr.State,
			"author":     pr.User.Login,
			"merged":     pr.Merged,
			"created_at": pr.CreatedAt,
			"updated_at": pr.UpdatedAt,
		}

		if err := s.upsertAndEmbed(ctx, artifactInput{
			ArtifactType: "pr", ExternalID: externalID,
			Title: pr.Title, Content: content, Metadata: metadata,
			SourceURL: pr.HTMLURL, CreatedAt: pr.CreatedAt,
		}); err != nil {
			slog.Error("failed to sync PR", "repo", fullName, "pr", pr.Number, "error", err)
			result.Errors++
			continue
		}
		result.Ingested++
	}

	s.setCursor(ctx, "github:prs:"+fullName, time.Now())
}

func (s *Syncer) syncCommits(ctx context.Context, fullName string, result *ingestion.SyncResult) {
	cursor := s.getCursor(ctx, "github:commits:"+fullName)

	commits, err := s.fetchCommits(ctx, fullName)
	if err != nil {
		slog.Error("failed to fetch commits", "repo", fullName, "error", err)
		result.Errors++
		return
	}

	for _, commit := range commits {
		commitDate := commit.Commit.Author.Date
		if !cursor.IsZero() && !commitDate.After(cursor) {
			result.Skipped++
			continue
		}

		firstLine := strings.SplitN(commit.Commit.Message, "\n", 2)[0]
		content := commit.Commit.Message

		detail, err := s.fetchCommitDetail(ctx, fullName, commit.SHA)
		if err != nil {
			slog.Debug("failed to fetch commit detail", "repo", fullName, "sha", commit.SHA[:8], "error", err)
		} else if len(detail.Files) > 0 {
			var files []string
			for _, f := range detail.Files {
				files = append(files, fmt.Sprintf("%s %s (+%d -%d)", f.Status, f.Filename, f.Additions, f.Deletions))
			}
			content += "\n\nFiles changed:\n" + strings.Join(files, "\n")
		}

		externalID := fmt.Sprintf("commit:%s:%s", fullName, commit.SHA)
		metadata := map[string]any{
			"repo":   fullName,
			"sha":    commit.SHA,
			"author": commit.Commit.Author.Name,
			"email":  commit.Commit.Author.Email,
			"date":   commitDate,
		}
		if detail != nil {
			metadata["additions"] = detail.Stats.Additions
			metadata["deletions"] = detail.Stats.Deletions
			metadata["files_changed"] = detail.Stats.Total
		}

		if err := s.upsertAndEmbed(ctx, artifactInput{
			ArtifactType: "commit", ExternalID: externalID,
			Title: firstLine, Content: content, Metadata: metadata,
			SourceURL: commit.HTMLURL, CreatedAt: commitDate,
		}); err != nil {
			slog.Error("failed to sync commit", "repo", fullName, "sha", commit.SHA[:8], "error", err)
			result.Errors++
			continue
		}
		result.Ingested++

		s.extractCommitReferences(ctx, externalID, fullName, commit.Commit.Message)
	}

	s.setCursor(ctx, "github:commits:"+fullName, time.Now())
}

func (s *Syncer) syncStarred(ctx context.Context, result *ingestion.SyncResult) {
	cursor := s.getCursor(ctx, "github:starred")

	starred, err := s.fetchStarred(ctx)
	if err != nil {
		slog.Error("failed to fetch starred repos", "error", err)
		result.Errors++
		return
	}

	for _, star := range starred {
		repo := star.Repo

		var starredAt time.Time
		if star.StarredAt != "" {
			starredAt, _ = time.Parse(time.RFC3339, star.StarredAt)
		}
		if !cursor.IsZero() && !starredAt.IsZero() && !starredAt.After(cursor) {
			result.Skipped++
			continue
		}

		readme, err := s.fetchReadme(ctx, repo.FullName)
		if err != nil {
			slog.Debug("no readme for starred repo", "repo", repo.FullName)
		}

		content := repo.Description
		if readme != "" {
			content += "\n\n" + readme
		}

		metadata := map[string]any{
			"owner":    repo.Owner.Login,
			"language": repo.Language,
			"topics":   repo.Topics,
			"stars":    repo.StarCount,
		}
		if !starredAt.IsZero() {
			metadata["starred_at"] = starredAt
		}

		if err := s.upsertAndEmbed(ctx, artifactInput{
			ArtifactType: "star", ExternalID: "star:" + repo.FullName,
			Title: repo.Name, Content: content, Metadata: metadata,
			SourceURL: repo.HTMLURL, CreatedAt: repo.CreatedAt,
		}); err != nil {
			slog.Error("failed to sync starred repo", "repo", repo.FullName, "error", err)
			result.Errors++
			continue
		}
		result.Ingested++
	}

	s.setCursor(ctx, "github:starred", time.Now())
}

func (s *Syncer) syncGists(ctx context.Context, result *ingestion.SyncResult) {
	cursor := s.getCursor(ctx, "github:gists")

	gists, err := s.fetchGists(ctx)
	if err != nil {
		slog.Error("failed to fetch gists", "error", err)
		result.Errors++
		return
	}

	for _, gist := range gists {
		if !cursor.IsZero() && !gist.UpdatedAt.After(cursor) {
			result.Skipped++
			continue
		}

		detail, err := s.fetchGistDetail(ctx, gist.ID)
		if err != nil {
			slog.Warn("failed to fetch gist detail", "gist", gist.ID, "error", err)
			result.Errors++
			continue
		}

		title, content, metadata := buildGistContent(detail)

		if err := s.upsertAndEmbed(ctx, artifactInput{
			ArtifactType: "gist", ExternalID: "gist:" + gist.ID,
			Title: title, Content: content, Metadata: metadata,
			SourceURL: gist.HTMLURL, CreatedAt: gist.CreatedAt,
		}); err != nil {
			slog.Error("failed to sync gist", "gist", gist.ID, "error", err)
			result.Errors++
			continue
		}
		result.Ingested++
	}

	s.setCursor(ctx, "github:gists", time.Now())
}

func buildGistContent(gist *ghGist) (title, content string, metadata map[string]any) {
	title = gist.Description
	var fileNames []string
	var contentParts []string
	for name, file := range gist.Files {
		fileNames = append(fileNames, name)
		if file.Content != "" {
			contentParts = append(contentParts, fmt.Sprintf("--- %s ---\n%s", name, file.Content))
		}
	}
	if title == "" && len(fileNames) > 0 {
		title = fileNames[0]
	}
	content = strings.Join(contentParts, "\n\n")
	metadata = map[string]any{
		"files":      fileNames,
		"public":     gist.Public,
		"created_at": gist.CreatedAt,
		"updated_at": gist.UpdatedAt,
	}
	return title, content, metadata
}

// --- Cross-reference extraction ---

func (s *Syncer) extractCommitReferences(ctx context.Context, commitExternalID, repoFullName, message string) {
	matches := issueRefRe.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return
	}

	commitArtifactID, err := s.getArtifactID(ctx, commitExternalID)
	if err != nil {
		return
	}

	for _, m := range matches {
		targetRepo := repoFullName
		if m[1] != "" {
			targetRepo = m[1]
		}
		issueNum := m[2]

		prExtID := fmt.Sprintf("pr:%s#%s", targetRepo, issueNum)
		targetID, err := s.getArtifactID(ctx, prExtID)
		if err != nil {
			continue
		}
		if targetID == commitArtifactID {
			continue
		}

		_, err = s.db.ExecContext(ctx, `
			INSERT INTO relationships (source_id, target_id, relation_type, confidence, metadata)
			VALUES ($1, $2, 'REFERENCES', 1.0, $3)
			ON CONFLICT (source_id, target_id, relation_type) DO NOTHING`,
			commitArtifactID, targetID, fmt.Sprintf(`{"ref": "%s#%s"}`, targetRepo, issueNum),
		)
		if err != nil {
			slog.Warn("failed to create commit reference", "commit", commitExternalID, "target", prExtID, "error", err)
		}
	}
}

// --- Helpers ---

type artifactInput struct {
	ArtifactType string
	ExternalID   string
	Title        string
	Content      string
	Metadata     map[string]any
	SourceURL    string
	CreatedAt    time.Time
}

func (s *Syncer) upsertAndEmbed(ctx context.Context, in artifactInput) error {
	hash := sha256Hash(in.Content)
	if s.isUnchanged(ctx, in.ExternalID, hash) {
		return nil
	}

	metadataJSON, err := json.Marshal(in.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	var id string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (source, artifact_type, external_id, title, content, metadata, content_hash, source_url, created_at)
		VALUES ('github', $1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			content_hash = EXCLUDED.content_hash,
			updated_at = NOW()
		RETURNING id`,
		in.ArtifactType, in.ExternalID, in.Title, in.Content, metadataJSON, hash, in.SourceURL, in.CreatedAt,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("upsert artifact: %w", err)
	}

	embeddingText := in.Title + "\n" + in.Content
	if err := s.embedSvc.EmbedArtifact(ctx, id, embeddingText); err != nil {
		slog.Warn("failed to generate embedding", "external_id", in.ExternalID, "error", err)
	}

	return nil
}

func (s *Syncer) isUnchanged(ctx context.Context, externalID, hash string) bool {
	var existingHash sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM artifacts WHERE source = 'github' AND external_id = $1`,
		externalID,
	).Scan(&existingHash)
	return err == nil && existingHash.Valid && existingHash.String == hash
}

func (s *Syncer) getArtifactID(ctx context.Context, externalID string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM artifacts WHERE source = 'github' AND external_id = $1`, externalID,
	).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Syncer) getCursor(ctx context.Context, name string) time.Time {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor_value FROM sync_cursors WHERE source_name = $1`, name,
	).Scan(&val)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, val)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *Syncer) setCursor(ctx context.Context, name string, t time.Time) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_cursors (source_name, cursor_value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (source_name) DO UPDATE SET
			cursor_value = EXCLUDED.cursor_value,
			updated_at = NOW()`,
		name, t.Format(time.RFC3339Nano),
	)
	if err != nil {
		slog.Warn("failed to update sync cursor", "name", name, "error", err)
	}
}

func sha256Hash(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

