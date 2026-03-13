package insights

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"pa/internal/digest"
	"pa/internal/llm"
)

// compile-time check that *Service implements digest.InsightProvider
var _ digest.InsightProvider = (*Service)(nil)

// Config controls which insights are generated and their parameters.
type Config struct {
	GemsLookbackDays     int     `yaml:"gems_lookback_days"`
	SerendipityLimit     int     `yaml:"serendipity_limit"`
	TopicWindowWeeks     int     `yaml:"topic_window_weeks"`
	DepthMinArtifacts    int     `yaml:"depth_min_artifacts"`
	VelocityRollingWeeks int     `yaml:"velocity_rolling_weeks"`
	MemoryLookbackMonths []int   `yaml:"memory_lookback_months"`
	SimilarityThreshold  float64 `yaml:"similarity_threshold"`
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.GemsLookbackDays <= 0 {
		out.GemsLookbackDays = 90
	}
	if out.SerendipityLimit <= 0 {
		out.SerendipityLimit = 5
	}
	if out.TopicWindowWeeks <= 0 {
		out.TopicWindowWeeks = 4
	}
	if out.DepthMinArtifacts <= 0 {
		out.DepthMinArtifacts = 2
	}
	if out.VelocityRollingWeeks <= 0 {
		out.VelocityRollingWeeks = 4
	}
	if len(out.MemoryLookbackMonths) == 0 {
		out.MemoryLookbackMonths = []int{1, 3, 6, 12}
	}
	if out.SimilarityThreshold <= 0 {
		out.SimilarityThreshold = 0.75
	}
	return out
}

// Service generates knowledge insights from the artifact database.
type Service struct {
	db       *sql.DB
	embedder llm.EmbeddingProvider
	cfg      Config
}

// NewService creates an insights service.
func NewService(db *sql.DB, embedder llm.EmbeddingProvider, cfg Config) *Service {
	resolved := cfg.withDefaults()
	return &Service{db: db, embedder: embedder, cfg: resolved}
}

// --- Forgotten Gems ---

// Gem is an older artifact that is semantically similar to recent activity.
type Gem struct {
	ID           string  `json:"id"`
	Source       string  `json:"source"`
	ArtifactType string  `json:"artifact_type"`
	Title        string  `json:"title"`
	Summary      *string `json:"summary,omitempty"`
	SourceURL    *string `json:"source_url,omitempty"`
	IngestedAt   string  `json:"ingested_at"`
	Similarity   float64 `json:"similarity"`
	MatchedTo    string  `json:"matched_to"`
}

type GemsResponse struct {
	Lookback string `json:"lookback"`
	Count    int    `json:"count"`
	Gems     []Gem  `json:"gems"`
}

// ForgottenGems finds older artifacts semantically similar to recent ones.
func (s *Service) ForgottenGems(ctx context.Context, lookbackDays int) (*GemsResponse, error) {
	if lookbackDays <= 0 {
		lookbackDays = s.cfg.GemsLookbackDays
	}

	now := time.Now().UTC()
	recentCutoff := now.AddDate(0, 0, -7)
	oldStart := now.AddDate(0, 0, -lookbackDays)
	threshold := s.cfg.SimilarityThreshold

	rows, err := s.db.QueryContext(ctx, `
		WITH recent AS (
			SELECT a.id, a.title, e.embedding
			FROM artifacts a
			JOIN artifact_embeddings e ON e.artifact_id = a.id
			WHERE a.ingested_at >= $1
			ORDER BY a.ingested_at DESC
			LIMIT 20
		),
		older AS (
			SELECT a.id, a.source, a.artifact_type, a.title, a.summary,
				a.source_url, a.ingested_at::text AS ingested_at, e.embedding
			FROM artifacts a
			JOIN artifact_embeddings e ON e.artifact_id = a.id
			WHERE a.ingested_at >= $2 AND a.ingested_at < $1
		)
		SELECT DISTINCT ON (o.id)
			o.id, o.source, o.artifact_type, o.title, o.summary,
			o.source_url, o.ingested_at,
			1 - (o.embedding <=> r.embedding) AS similarity,
			r.title AS matched_to
		FROM older o
		CROSS JOIN recent r
		WHERE 1 - (o.embedding <=> r.embedding) >= $3
		ORDER BY o.id, similarity DESC
	`, recentCutoff, oldStart, threshold)
	if err != nil {
		return nil, fmt.Errorf("query forgotten gems: %w", err)
	}
	defer rows.Close()

	var gems []Gem
	for rows.Next() {
		var g Gem
		if err := rows.Scan(
			&g.ID, &g.Source, &g.ArtifactType, &g.Title, &g.Summary,
			&g.SourceURL, &g.IngestedAt, &g.Similarity, &g.MatchedTo,
		); err != nil {
			return nil, fmt.Errorf("scan gem: %w", err)
		}
		gems = append(gems, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(gems, func(i, j int) bool {
		return gems[i].Similarity > gems[j].Similarity
	})

	limit := 10
	if len(gems) > limit {
		gems = gems[:limit]
	}
	if gems == nil {
		gems = []Gem{}
	}

	return &GemsResponse{
		Lookback: fmt.Sprintf("%dd", lookbackDays),
		Count:    len(gems),
		Gems:     gems,
	}, nil
}

// --- Serendipity Highlights ---

// SerendipityItem is a surprising cross-source connection.
type SerendipityItem struct {
	SourceTitle  string  `json:"source_title"`
	SourceType   string  `json:"source_type"`
	TargetTitle  string  `json:"target_title"`
	TargetType   string  `json:"target_type"`
	RelationType string  `json:"relation_type"`
	Confidence   float64 `json:"confidence"`
	Score        float64 `json:"score"`
}

type SerendipityResponse struct {
	Period string            `json:"period"`
	Count  int               `json:"count"`
	Items  []SerendipityItem `json:"items"`
}

var sourceDiversityBoost = map[string]float64{
	"paper-repo":     1.5,
	"paper-video":    1.4,
	"video-repo":     1.3,
	"paper-note":     1.3,
	"note-repo":      1.2,
	"trending-paper": 1.4,
}

func diversityMultiplier(sourceType, targetType string) float64 {
	if sourceType == targetType {
		return 0.7
	}
	key := sourceType + "-" + targetType
	if m, ok := sourceDiversityBoost[key]; ok {
		return m
	}
	key = targetType + "-" + sourceType
	if m, ok := sourceDiversityBoost[key]; ok {
		return m
	}
	return 1.0
}

func classifySourceType(source string) string {
	switch source {
	case "arxiv", "springer":
		return "paper"
	case "github":
		return "repo"
	case "github_trending":
		return "trending"
	case "youtube":
		return "video"
	case "filesystem":
		return "note"
	case "onedrive":
		return "document"
	default:
		return source
	}
}

// Serendipity finds the most surprising cross-source connections in a period.
func (s *Service) Serendipity(ctx context.Context, tr digest.TimeRange) (*SerendipityResponse, error) {
	limit := s.cfg.SerendipityLimit

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
		LIMIT 50
	`, tr.From, tr.To)
	if err != nil {
		return nil, fmt.Errorf("query serendipity: %w", err)
	}
	defer rows.Close()

	var items []SerendipityItem
	for rows.Next() {
		var item SerendipityItem
		if err := rows.Scan(
			&item.SourceTitle, &item.SourceType,
			&item.TargetTitle, &item.TargetType,
			&item.RelationType, &item.Confidence,
		); err != nil {
			return nil, fmt.Errorf("scan serendipity: %w", err)
		}
		srcClass := classifySourceType(item.SourceType)
		tgtClass := classifySourceType(item.TargetType)
		item.Score = item.Confidence * diversityMultiplier(srcClass, tgtClass)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})

	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []SerendipityItem{}
	}

	return &SerendipityResponse{
		Period: tr.Label(),
		Count:  len(items),
		Items:  items,
	}, nil
}

// --- Topic Momentum / Drift ---

// TopicTrend represents a topic's momentum over time.
type TopicTrend struct {
	Tag            string  `json:"tag"`
	CurrentCount   int     `json:"current_count"`
	PreviousCount  int     `json:"previous_count"`
	ChangePercent  float64 `json:"change_percent"`
	Momentum       string  `json:"momentum"`
	SourceCount    int     `json:"source_count"`
}

type TopicsResponse struct {
	Period   string       `json:"period"`
	Gaining  []TopicTrend `json:"gaining"`
	Cooling  []TopicTrend `json:"cooling"`
	Steady   []TopicTrend `json:"steady"`
}

// TopicMomentum computes tag frequency changes between current and previous period.
func (s *Service) TopicMomentum(ctx context.Context, windowWeeks int) (*TopicsResponse, error) {
	if windowWeeks <= 0 {
		windowWeeks = s.cfg.TopicWindowWeeks
	}

	now := time.Now().UTC()
	currentFrom := now.AddDate(0, 0, -windowWeeks*7)
	previousFrom := currentFrom.AddDate(0, 0, -windowWeeks*7)

	currentCounts, err := s.tagCountsInWindow(ctx, currentFrom, now)
	if err != nil {
		return nil, fmt.Errorf("current tag counts: %w", err)
	}

	previousCounts, err := s.tagCountsInWindow(ctx, previousFrom, currentFrom)
	if err != nil {
		return nil, fmt.Errorf("previous tag counts: %w", err)
	}

	sourceCounts, err := s.tagSourceDiversity(ctx, currentFrom, now)
	if err != nil {
		return nil, fmt.Errorf("tag source diversity: %w", err)
	}

	allTags := make(map[string]bool)
	for t := range currentCounts {
		allTags[t] = true
	}
	for t := range previousCounts {
		allTags[t] = true
	}

	var gaining, cooling, steady []TopicTrend
	for tag := range allTags {
		cur := currentCounts[tag]
		prev := previousCounts[tag]

		var changePct float64
		if prev > 0 {
			changePct = float64(cur-prev) / float64(prev) * 100
		} else if cur > 0 {
			changePct = 100.0
		}

		momentum := "steady"
		if changePct > 25 {
			momentum = "gaining"
		} else if changePct < -25 {
			momentum = "cooling"
		}

		trend := TopicTrend{
			Tag:           tag,
			CurrentCount:  cur,
			PreviousCount: prev,
			ChangePercent: math.Round(changePct*10) / 10,
			Momentum:      momentum,
			SourceCount:   sourceCounts[tag],
		}

		switch momentum {
		case "gaining":
			gaining = append(gaining, trend)
		case "cooling":
			cooling = append(cooling, trend)
		default:
			steady = append(steady, trend)
		}
	}

	sort.Slice(gaining, func(i, j int) bool { return gaining[i].ChangePercent > gaining[j].ChangePercent })
	sort.Slice(cooling, func(i, j int) bool { return cooling[i].ChangePercent < cooling[j].ChangePercent })
	sort.Slice(steady, func(i, j int) bool { return steady[i].CurrentCount > steady[j].CurrentCount })

	if gaining == nil {
		gaining = []TopicTrend{}
	}
	if cooling == nil {
		cooling = []TopicTrend{}
	}
	if steady == nil {
		steady = []TopicTrend{}
	}

	return &TopicsResponse{
		Period:  fmt.Sprintf("last %d weeks", windowWeeks),
		Gaining: gaining,
		Cooling: cooling,
		Steady:  steady,
	}, nil
}

func (s *Service) tagCountsInWindow(ctx context.Context, from, to time.Time) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.tag, count(*)
		FROM tags t
		JOIN artifacts a ON a.id = t.artifact_id
		WHERE a.ingested_at >= $1 AND a.ingested_at < $2
			AND t.auto_generated = true
		GROUP BY t.tag
		ORDER BY count(*) DESC
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var tag string
		var count int
		if err := rows.Scan(&tag, &count); err != nil {
			return nil, err
		}
		counts[tag] = count
	}
	return counts, rows.Err()
}

func (s *Service) tagSourceDiversity(ctx context.Context, from, to time.Time) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.tag, count(DISTINCT a.source)
		FROM tags t
		JOIN artifacts a ON a.id = t.artifact_id
		WHERE a.ingested_at >= $1 AND a.ingested_at < $2
			AND t.auto_generated = true
		GROUP BY t.tag
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var tag string
		var count int
		if err := rows.Scan(&tag, &count); err != nil {
			return nil, err
		}
		counts[tag] = count
	}
	return counts, rows.Err()
}

// --- Knowledge Depth Map ---

// DepthEntry represents topic depth (artifact count × source diversity).
type DepthEntry struct {
	Tag           string   `json:"tag"`
	ArtifactCount int      `json:"artifact_count"`
	SourceCount   int      `json:"source_count"`
	Sources       []string `json:"sources"`
	DepthScore    float64  `json:"depth_score"`
	Classification string  `json:"classification"`
}

type DepthResponse struct {
	Count   int          `json:"count"`
	Entries []DepthEntry `json:"entries"`
}

// KnowledgeDepth calculates per-topic depth across the knowledge base.
func (s *Service) KnowledgeDepth(ctx context.Context) (*DepthResponse, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			t.tag,
			count(DISTINCT t.artifact_id) AS artifact_count,
			count(DISTINCT a.source) AS source_count,
			array_agg(DISTINCT a.source) AS sources
		FROM tags t
		JOIN artifacts a ON a.id = t.artifact_id
		WHERE t.auto_generated = true
		GROUP BY t.tag
		HAVING count(DISTINCT t.artifact_id) >= $1
		ORDER BY count(DISTINCT t.artifact_id) * count(DISTINCT a.source) DESC
	`, s.cfg.DepthMinArtifacts)
	if err != nil {
		return nil, fmt.Errorf("query depth: %w", err)
	}
	defer rows.Close()

	var entries []DepthEntry
	for rows.Next() {
		var e DepthEntry
		var sources string
		if err := rows.Scan(&e.Tag, &e.ArtifactCount, &e.SourceCount, &sources); err != nil {
			return nil, fmt.Errorf("scan depth: %w", err)
		}
		e.Sources = parsePostgresArray(sources)
		e.DepthScore = float64(e.ArtifactCount) * float64(e.SourceCount)
		e.Classification = classifyDepth(e.ArtifactCount, e.SourceCount)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []DepthEntry{}
	}

	return &DepthResponse{
		Count:   len(entries),
		Entries: entries,
	}, nil
}

func classifyDepth(artifactCount, sourceCount int) string {
	if artifactCount >= 5 && sourceCount >= 3 {
		return "deep"
	}
	if artifactCount >= 3 || sourceCount >= 2 {
		return "moderate"
	}
	return "shallow"
}

func parsePostgresArray(s string) []string {
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// --- Learning Velocity ---

// VelocityPeriod represents ingestion counts for a single time window.
type VelocityPeriod struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Total    int            `json:"total"`
	BySource map[string]int `json:"by_source"`
	ByType   map[string]int `json:"by_type"`
}

type VelocityResponse struct {
	Current        VelocityPeriod `json:"current"`
	Previous       VelocityPeriod `json:"previous"`
	RollingAverage VelocityPeriod `json:"rolling_average"`
	ChangePercent  float64        `json:"change_percent"`
	Summary        string         `json:"summary"`
}

// LearningVelocity compares current period ingestion to previous and rolling average.
func (s *Service) LearningVelocity(ctx context.Context, tr digest.TimeRange) (*VelocityResponse, error) {
	days := tr.Days()
	if days <= 0 {
		days = 7
	}

	previousTR := digest.TimeRange{
		From: tr.From.AddDate(0, 0, -days),
		To:   tr.From,
	}

	rollingWeeks := s.cfg.VelocityRollingWeeks
	rollingTR := digest.TimeRange{
		From: tr.From.AddDate(0, 0, -rollingWeeks*7),
		To:   tr.From,
	}

	current, err := s.countArtifactsInWindow(ctx, tr.From, tr.To)
	if err != nil {
		return nil, fmt.Errorf("current velocity: %w", err)
	}

	previous, err := s.countArtifactsInWindow(ctx, previousTR.From, previousTR.To)
	if err != nil {
		return nil, fmt.Errorf("previous velocity: %w", err)
	}

	rolling, err := s.countArtifactsInWindow(ctx, rollingTR.From, rollingTR.To)
	if err != nil {
		return nil, fmt.Errorf("rolling velocity: %w", err)
	}

	avgTotal := 0
	if rollingWeeks > 0 {
		avgTotal = rolling.Total * days / (rollingWeeks * 7)
	}

	var changePct float64
	if previous.Total > 0 {
		changePct = float64(current.Total-previous.Total) / float64(previous.Total) * 100
	} else if current.Total > 0 {
		changePct = 100.0
	}
	changePct = math.Round(changePct*10) / 10

	summary := buildVelocitySummary(current.Total, previous.Total, avgTotal, days)

	rollingAvg := VelocityPeriod{
		From:     rollingTR.From.Format("2006-01-02"),
		To:       rollingTR.To.Format("2006-01-02"),
		Total:    avgTotal,
		BySource: averageMap(rolling.BySource, rollingWeeks*7, days),
		ByType:   averageMap(rolling.ByType, rollingWeeks*7, days),
	}

	return &VelocityResponse{
		Current: VelocityPeriod{
			From:     tr.From.Format("2006-01-02"),
			To:       tr.To.Format("2006-01-02"),
			Total:    current.Total,
			BySource: current.BySource,
			ByType:   current.ByType,
		},
		Previous: VelocityPeriod{
			From:     previousTR.From.Format("2006-01-02"),
			To:       previousTR.To.Format("2006-01-02"),
			Total:    previous.Total,
			BySource: previous.BySource,
			ByType:   previous.ByType,
		},
		RollingAverage: rollingAvg,
		ChangePercent:  changePct,
		Summary:        summary,
	}, nil
}

type windowCounts struct {
	Total    int
	BySource map[string]int
	ByType   map[string]int
}

func (s *Service) countArtifactsInWindow(ctx context.Context, from, to time.Time) (windowCounts, error) {
	wc := windowCounts{
		BySource: make(map[string]int),
		ByType:   make(map[string]int),
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT source, artifact_type, count(*)
		FROM artifacts
		WHERE ingested_at >= $1 AND ingested_at < $2
		GROUP BY source, artifact_type
	`, from, to)
	if err != nil {
		return wc, err
	}
	defer rows.Close()

	for rows.Next() {
		var source, atype string
		var count int
		if err := rows.Scan(&source, &atype, &count); err != nil {
			return wc, err
		}
		wc.Total += count
		wc.BySource[source] += count
		wc.ByType[atype] += count
	}
	return wc, rows.Err()
}

func averageMap(m map[string]int, totalDays, periodDays int) map[string]int {
	if totalDays <= 0 {
		return map[string]int{}
	}
	avg := make(map[string]int, len(m))
	for k, v := range m {
		avg[k] = v * periodDays / totalDays
	}
	return avg
}

func buildVelocitySummary(current, previous, avg, days int) string {
	periodName := "week"
	if days == 1 {
		periodName = "day"
	} else if days >= 28 {
		periodName = "month"
	}

	if current == 0 && previous == 0 {
		return fmt.Sprintf("No artifacts ingested this %s or last.", periodName)
	}

	parts := []string{fmt.Sprintf("%d artifacts this %s", current, periodName)}

	if previous > 0 {
		if current > previous {
			parts = append(parts, fmt.Sprintf("up from %d last %s", previous, periodName))
		} else if current < previous {
			parts = append(parts, fmt.Sprintf("down from %d last %s", previous, periodName))
		} else {
			parts = append(parts, fmt.Sprintf("same as last %s", periodName))
		}
	}

	if avg > 0 && avg != current {
		parts = append(parts, fmt.Sprintf("vs your usual %d", avg))
	}

	return strings.Join(parts, ", ") + "."
}

// --- Memories (This Time Last Month/Year) ---

// MemoryPeriod groups artifacts from a historical period.
type MemoryPeriod struct {
	Label     string           `json:"label"`
	From      string           `json:"from"`
	To        string           `json:"to"`
	Count     int              `json:"count"`
	Artifacts []MemoryArtifact `json:"artifacts"`
}

type MemoryArtifact struct {
	ID           string  `json:"id"`
	Source       string  `json:"source"`
	ArtifactType string  `json:"artifact_type"`
	Title        string  `json:"title"`
	Summary      *string `json:"summary,omitempty"`
	SourceURL    *string `json:"source_url,omitempty"`
}

type MemoriesResponse struct {
	Periods []MemoryPeriod `json:"periods"`
}

// Memories retrieves artifacts from the same calendar window in previous months/years.
func (s *Service) Memories(ctx context.Context, lookbackMonths []int) (*MemoriesResponse, error) {
	if len(lookbackMonths) == 0 {
		lookbackMonths = s.cfg.MemoryLookbackMonths
	}

	now := time.Now().UTC()
	windowDays := 7

	var periods []MemoryPeriod
	for _, months := range lookbackMonths {
		center := now.AddDate(0, -months, 0)
		from := center.AddDate(0, 0, -windowDays/2)
		to := center.AddDate(0, 0, windowDays/2+1)

		artifacts, err := s.queryMemoryArtifacts(ctx, from, to)
		if err != nil {
			slog.Warn("memory query failed", "months_ago", months, "error", err)
			continue
		}

		if len(artifacts) == 0 {
			continue
		}

		label := fmt.Sprintf("%d month ago", months)
		if months != 1 {
			label = fmt.Sprintf("%d months ago", months)
		}
		if months == 12 {
			label = "1 year ago"
		}

		periods = append(periods, MemoryPeriod{
			Label:     label,
			From:      from.Format("2006-01-02"),
			To:        to.Format("2006-01-02"),
			Count:     len(artifacts),
			Artifacts: artifacts,
		})
	}

	if periods == nil {
		periods = []MemoryPeriod{}
	}

	return &MemoriesResponse{Periods: periods}, nil
}

func (s *Service) queryMemoryArtifacts(ctx context.Context, from, to time.Time) ([]MemoryArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, artifact_type, title, summary, source_url
		FROM artifacts
		WHERE ingested_at >= $1 AND ingested_at < $2
		ORDER BY ingested_at DESC
		LIMIT 10
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []MemoryArtifact
	for rows.Next() {
		var a MemoryArtifact
		if err := rows.Scan(&a.ID, &a.Source, &a.ArtifactType, &a.Title, &a.Summary, &a.SourceURL); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, rows.Err()
}

// AllInsights gathers all insight sections for digest integration.
type AllInsights struct {
	Gems        *GemsResponse        `json:"gems,omitempty"`
	Serendipity *SerendipityResponse `json:"serendipity,omitempty"`
	Topics      *TopicsResponse      `json:"topics,omitempty"`
	Depth       *DepthResponse       `json:"depth,omitempty"`
	Velocity    *VelocityResponse    `json:"velocity,omitempty"`
	Memories    *MemoriesResponse    `json:"memories,omitempty"`
}
