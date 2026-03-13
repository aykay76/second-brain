package digest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"pa/internal/llm"
)

// Config holds digest generation settings.
type Config struct {
	DefaultPeriod Period `yaml:"default_period"`
	WeekStartDay  string `yaml:"week_start_day"`
}

// Service generates knowledge base digests over time windows.
type Service struct {
	db   *sql.DB
	chat llm.ChatProvider
	cfg  Config
}

// NewService creates a digest service.
func NewService(db *sql.DB, chat llm.ChatProvider, cfg Config) *Service {
	if cfg.DefaultPeriod == "" {
		cfg.DefaultPeriod = PeriodWeekly
	}
	return &Service{db: db, chat: chat, cfg: cfg}
}

// DigestRequest specifies what digest to generate.
type DigestRequest struct {
	Period    Period     `json:"period,omitempty"`
	From      *string    `json:"from,omitempty"`
	To        *string    `json:"to,omitempty"`
	NaturalTZ string     `json:"natural,omitempty"`
	Now       time.Time  `json:"-"`
}

// DigestResponse is the full digest output.
type DigestResponse struct {
	TimeRange     TimeRange              `json:"time_range"`
	Label         string                 `json:"label"`
	Narrative     string                 `json:"narrative"`
	Activity      ActivitySummary        `json:"activity"`
	TopArtifacts  []DigestArtifact       `json:"top_artifacts"`
	Connections   []DigestConnection     `json:"connections"`
	SourceBreakdown map[string]int       `json:"source_breakdown"`
}

// ActivitySummary holds aggregate counts.
type ActivitySummary struct {
	TotalIngested    int            `json:"total_ingested"`
	BySource         map[string]int `json:"by_source"`
	ByType           map[string]int `json:"by_type"`
	NewRelationships int            `json:"new_relationships"`
}

// DigestArtifact is a summary-level artifact for the digest.
type DigestArtifact struct {
	ID           string  `json:"id"`
	Source       string  `json:"source"`
	ArtifactType string  `json:"artifact_type"`
	Title        string  `json:"title"`
	Summary      *string `json:"summary,omitempty"`
	SourceURL    *string `json:"source_url,omitempty"`
	IngestedAt   string  `json:"ingested_at"`
}

// DigestConnection is a cross-source relationship discovered in the window.
type DigestConnection struct {
	SourceTitle    string  `json:"source_title"`
	SourceType     string  `json:"source_type"`
	TargetTitle    string  `json:"target_title"`
	TargetType     string  `json:"target_type"`
	RelationType   string  `json:"relation_type"`
	Confidence     float64 `json:"confidence"`
}

// Generate builds a digest for the resolved time range.
func (s *Service) Generate(ctx context.Context, req DigestRequest) (*DigestResponse, error) {
	tr, err := s.resolveTimeRange(req)
	if err != nil {
		return nil, fmt.Errorf("resolve time range: %w", err)
	}

	activity, err := s.queryActivity(ctx, tr)
	if err != nil {
		return nil, fmt.Errorf("query activity: %w", err)
	}

	topArtifacts, err := s.queryTopArtifacts(ctx, tr)
	if err != nil {
		return nil, fmt.Errorf("query top artifacts: %w", err)
	}

	connections, err := s.queryConnections(ctx, tr)
	if err != nil {
		return nil, fmt.Errorf("query connections: %w", err)
	}

	narrative, err := s.generateNarrative(ctx, tr, activity, topArtifacts, connections)
	if err != nil {
		slog.Warn("narrative generation failed, using fallback", "error", err)
		narrative = s.fallbackNarrative(tr, activity)
	}

	return &DigestResponse{
		TimeRange:       tr,
		Label:           tr.Label(),
		Narrative:       narrative,
		Activity:        activity,
		TopArtifacts:    topArtifacts,
		Connections:     connections,
		SourceBreakdown: activity.BySource,
	}, nil
}

func (s *Service) resolveTimeRange(req DigestRequest) (TimeRange, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}

	if req.From != nil && req.To != nil {
		from, err := parseFlexibleDate(*req.From)
		if err != nil {
			return TimeRange{}, fmt.Errorf("parse from: %w", err)
		}
		to, err := parseFlexibleDate(*req.To)
		if err != nil {
			return TimeRange{}, fmt.Errorf("parse to: %w", err)
		}
		return TimeRange{From: from, To: to}, nil
	}

	if req.NaturalTZ != "" {
		return ParseNaturalDate(req.NaturalTZ, now)
	}

	period := req.Period
	if period == "" {
		period = s.cfg.DefaultPeriod
	}
	return ResolvePeriod(period, now), nil
}

func parseFlexibleDate(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02",
		"2006-01-02T15:04:05Z",
		time.RFC3339,
		"2006-01",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date format: %q", s)
}

func (s *Service) queryActivity(ctx context.Context, tr TimeRange) (ActivitySummary, error) {
	var summary ActivitySummary
	summary.BySource = make(map[string]int)
	summary.ByType = make(map[string]int)

	rows, err := s.db.QueryContext(ctx, `
		SELECT source, artifact_type, count(*)
		FROM artifacts
		WHERE ingested_at >= $1 AND ingested_at < $2
		GROUP BY source, artifact_type
		ORDER BY count(*) DESC
	`, tr.From, tr.To)
	if err != nil {
		return summary, fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var source, artifactType string
		var count int
		if err := rows.Scan(&source, &artifactType, &count); err != nil {
			return summary, err
		}
		summary.TotalIngested += count
		summary.BySource[source] += count
		summary.ByType[artifactType] += count
	}
	if err := rows.Err(); err != nil {
		return summary, err
	}

	err = s.db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM relationships
		WHERE created_at >= $1 AND created_at < $2
	`, tr.From, tr.To).Scan(&summary.NewRelationships)
	if err != nil {
		return summary, fmt.Errorf("query relationships: %w", err)
	}

	return summary, nil
}

func (s *Service) queryTopArtifacts(ctx context.Context, tr TimeRange) ([]DigestArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, artifact_type, title, summary, source_url, ingested_at::text
		FROM artifacts
		WHERE ingested_at >= $1 AND ingested_at < $2
		ORDER BY ingested_at DESC
		LIMIT 20
	`, tr.From, tr.To)
	if err != nil {
		return nil, fmt.Errorf("query top artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []DigestArtifact
	for rows.Next() {
		var a DigestArtifact
		if err := rows.Scan(
			&a.ID, &a.Source, &a.ArtifactType, &a.Title,
			&a.Summary, &a.SourceURL, &a.IngestedAt,
		); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	if artifacts == nil {
		artifacts = []DigestArtifact{}
	}
	return artifacts, rows.Err()
}

func (s *Service) queryConnections(ctx context.Context, tr TimeRange) ([]DigestConnection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			src.title, src.source,
			tgt.title, tgt.source,
			r.relation_type, COALESCE(r.confidence, 0)
		FROM relationships r
		JOIN artifacts src ON src.id = r.source_id
		JOIN artifacts tgt ON tgt.id = r.target_id
		WHERE r.created_at >= $1 AND r.created_at < $2
			AND src.source != tgt.source
		ORDER BY r.confidence DESC NULLS LAST
		LIMIT 10
	`, tr.From, tr.To)
	if err != nil {
		return nil, fmt.Errorf("query connections: %w", err)
	}
	defer rows.Close()

	var connections []DigestConnection
	for rows.Next() {
		var c DigestConnection
		if err := rows.Scan(
			&c.SourceTitle, &c.SourceType,
			&c.TargetTitle, &c.TargetType,
			&c.RelationType, &c.Confidence,
		); err != nil {
			return nil, err
		}
		connections = append(connections, c)
	}
	if connections == nil {
		connections = []DigestConnection{}
	}
	return connections, rows.Err()
}

const digestSystemPrompt = `You are a personal knowledge assistant generating a periodic digest.
Write a brief, engaging narrative (3-5 sentences) summarising what the user worked on,
learned, and saved during this period. Mention specific topics and highlight any
interesting cross-source connections. Be conversational but concise.
Do NOT use bullet points — write flowing prose.`

func (s *Service) generateNarrative(
	ctx context.Context,
	tr TimeRange,
	activity ActivitySummary,
	artifacts []DigestArtifact,
	connections []DigestConnection,
) (string, error) {
	if s.chat == nil {
		return s.fallbackNarrative(tr, activity), nil
	}

	context := buildNarrativeContext(tr, activity, artifacts, connections)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: digestSystemPrompt},
		{Role: llm.RoleUser, Content: context},
	}

	return s.chat.Complete(ctx, messages)
}

func buildNarrativeContext(
	tr TimeRange,
	activity ActivitySummary,
	artifacts []DigestArtifact,
	connections []DigestConnection,
) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Period: %s\n", tr.Label())
	fmt.Fprintf(&b, "Total artifacts ingested: %d\n\n", activity.TotalIngested)

	if len(activity.BySource) > 0 {
		b.WriteString("Activity by source:\n")
		for source, count := range activity.BySource {
			fmt.Fprintf(&b, "  - %s: %d items\n", source, count)
		}
		b.WriteString("\n")
	}

	if len(artifacts) > 0 {
		b.WriteString("Notable artifacts:\n")
		limit := 10
		if len(artifacts) < limit {
			limit = len(artifacts)
		}
		for _, a := range artifacts[:limit] {
			summary := ""
			if a.Summary != nil && *a.Summary != "" {
				summary = " — " + truncateStr(*a.Summary, 100)
			}
			fmt.Fprintf(&b, "  - [%s/%s] %s%s\n", a.Source, a.ArtifactType, a.Title, summary)
		}
		b.WriteString("\n")
	}

	if len(connections) > 0 {
		b.WriteString("Cross-source connections discovered:\n")
		for _, c := range connections {
			fmt.Fprintf(&b, "  - %s (%s) %s %s (%s) [confidence: %.0f%%]\n",
				c.SourceTitle, c.SourceType, c.RelationType,
				c.TargetTitle, c.TargetType, c.Confidence*100)
		}
	}

	return b.String()
}

func (s *Service) fallbackNarrative(tr TimeRange, activity ActivitySummary) string {
	if activity.TotalIngested == 0 {
		return fmt.Sprintf("No new artifacts were ingested during %s.", tr.Label())
	}

	parts := make([]string, 0, len(activity.BySource))
	sources := sortedSourceKeys(activity.BySource)
	for _, source := range sources {
		count := activity.BySource[source]
		parts = append(parts, fmt.Sprintf("%d from %s", count, source))
	}

	return fmt.Sprintf("During %s, you ingested %d artifacts: %s. %d new connections were discovered.",
		tr.Label(), activity.TotalIngested, strings.Join(parts, ", "), activity.NewRelationships)
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func sortedSourceKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return m[keys[i]] > m[keys[j]]
	})
	return keys
}

// FormatMarkdown renders the digest as a markdown document.
func FormatMarkdown(d *DigestResponse) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Knowledge Digest: %s\n\n", d.Label)
	fmt.Fprintf(&b, "> Generated %s\n\n", time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "%s\n\n", d.Narrative)

	writeActivitySection(&b, d.Activity)
	writeArtifactsSection(&b, d.TopArtifacts)
	writeConnectionsSection(&b, d.Connections)

	return b.String()
}

func writeActivitySection(b *strings.Builder, activity ActivitySummary) {
	fmt.Fprintf(b, "## Activity Summary\n\n")
	fmt.Fprintf(b, "**%d artifacts** ingested | **%d connections** discovered\n\n",
		activity.TotalIngested, activity.NewRelationships)

	if len(activity.BySource) > 0 {
		b.WriteString("| Source | Count |\n|---|---|\n")
		for _, source := range sortedSourceKeys(activity.BySource) {
			fmt.Fprintf(b, "| %s | %d |\n", source, activity.BySource[source])
		}
		b.WriteString("\n")
	}
}

func writeArtifactsSection(b *strings.Builder, artifacts []DigestArtifact) {
	if len(artifacts) == 0 {
		return
	}
	fmt.Fprintf(b, "## Recent Artifacts\n\n")
	for _, a := range artifacts {
		fmt.Fprintf(b, "- **%s** `%s/%s`%s%s\n",
			a.Title, a.Source, a.ArtifactType,
			formatOptionalURL(a.SourceURL),
			formatOptionalSummary(a.Summary))
	}
	b.WriteString("\n")
}

func writeConnectionsSection(b *strings.Builder, connections []DigestConnection) {
	if len(connections) == 0 {
		return
	}
	fmt.Fprintf(b, "## Cross-Source Connections\n\n")
	for _, c := range connections {
		fmt.Fprintf(b, "- **%s** (%s) ← %s → **%s** (%s) — %.0f%% confidence\n",
			c.SourceTitle, c.SourceType, c.RelationType,
			c.TargetTitle, c.TargetType, c.Confidence*100)
	}
	b.WriteString("\n")
}

func formatOptionalURL(u *string) string {
	if u != nil && *u != "" {
		return fmt.Sprintf(" — [link](%s)", *u)
	}
	return ""
}

func formatOptionalSummary(s *string) string {
	if s != nil && *s != "" {
		return "\n  > " + truncateStr(*s, 200)
	}
	return ""
}

// QueryArtifactsByTimeRange returns artifacts within a time range with optional filters.
func QueryArtifactsByTimeRange(ctx context.Context, db *sql.DB, tr TimeRange, source, artifactType string, limit int) ([]DigestArtifact, error) {
	if limit <= 0 {
		limit = 50
	}

	var conditions []string
	var args []any
	argIdx := 1

	if !tr.From.IsZero() {
		conditions = append(conditions, fmt.Sprintf("ingested_at >= $%d", argIdx))
		args = append(args, tr.From)
		argIdx++
	}
	conditions = append(conditions, fmt.Sprintf("ingested_at < $%d", argIdx))
	args = append(args, tr.To)
	argIdx++

	if source != "" {
		conditions = append(conditions, fmt.Sprintf("source = $%d", argIdx))
		args = append(args, source)
		argIdx++
	}
	if artifactType != "" {
		conditions = append(conditions, fmt.Sprintf("artifact_type = $%d", argIdx))
		args = append(args, artifactType)
		argIdx++
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT id, source, artifact_type, title, summary, source_url, ingested_at::text
		FROM artifacts %s
		ORDER BY ingested_at DESC
		LIMIT $%d
	`, where, argIdx)
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DigestArtifact
	for rows.Next() {
		var a DigestArtifact
		if err := rows.Scan(&a.ID, &a.Source, &a.ArtifactType, &a.Title, &a.Summary, &a.SourceURL, &a.IngestedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if out == nil {
		out = []DigestArtifact{}
	}
	return out, rows.Err()
}

// marshalJSON is a helper for tests.
func marshalJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
