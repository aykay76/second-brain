package cli

import "encoding/json"

type HealthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

type StatusResponse struct {
	Artifacts struct {
		Total    int            `json:"total"`
		BySource map[string]int `json:"by_source"`
		ByType   map[string]int `json:"by_type"`
	} `json:"artifacts"`
	Embeddings struct {
		Total    int     `json:"total"`
		Coverage float64 `json:"coverage"`
	} `json:"embeddings"`
	Relationships struct {
		Total  int            `json:"total"`
		ByType map[string]int `json:"by_type"`
	} `json:"relationships"`
	SyncCursors []struct {
		SourceName  string `json:"source_name"`
		CursorValue string `json:"cursor_value"`
		UpdatedAt   string `json:"updated_at"`
	} `json:"sync_cursors"`
}

type SearchResult struct {
	ID           string          `json:"id"`
	Source       string          `json:"source"`
	ArtifactType string         `json:"artifact_type"`
	Title        string          `json:"title"`
	Content      *string         `json:"content,omitempty"`
	Summary      *string         `json:"summary,omitempty"`
	SourceURL    *string         `json:"source_url,omitempty"`
	Score        float64         `json:"score"`
	Metadata     json.RawMessage `json:"metadata"`
}

type SearchResponse struct {
	Query   string         `json:"query"`
	Count   int            `json:"count"`
	Results []SearchResult `json:"results"`
}

type AskSource struct {
	Index        int     `json:"index"`
	ArtifactID   string  `json:"artifact_id"`
	Title        string  `json:"title"`
	Source       string  `json:"source"`
	ArtifactType string  `json:"artifact_type"`
	SourceURL    *string `json:"source_url,omitempty"`
	Score        float64 `json:"score"`
	Cited        bool    `json:"cited"`
}

type AskResponse struct {
	Question string      `json:"question"`
	Answer   string      `json:"answer"`
	Sources  []AskSource `json:"sources"`
}

type IngestResponse struct {
	Source   string `json:"source"`
	Ingested int    `json:"ingested"`
	Skipped  int    `json:"skipped"`
	Errors   int    `json:"errors"`
}

type VisionJobResponse struct {
	Message string `json:"message"`
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
}

type VisionJobStatus struct {
	ID              string `json:"id"`
	StartedAt       string `json:"started_at"`
	Done            bool   `json:"done"`
	ElapsedSeconds  int    `json:"elapsed_seconds"`
	Ingested        int    `json:"ingested,omitempty"`
	Skipped         int    `json:"skipped,omitempty"`
	Errors          int    `json:"errors,omitempty"`
	Error           string `json:"error,omitempty"`
}

type Artifact struct {
	ID           string          `json:"id"`
	Source       string          `json:"source"`
	ArtifactType string         `json:"artifact_type"`
	Title        string          `json:"title"`
	Summary      *string         `json:"summary,omitempty"`
	SourceURL    *string         `json:"source_url,omitempty"`
	Metadata     json.RawMessage `json:"metadata"`
	CreatedAt    string          `json:"created_at"`
	IngestedAt   string          `json:"ingested_at"`
}

type ArtifactListResponse struct {
	Count     int        `json:"count"`
	Artifacts []Artifact `json:"artifacts"`
}

type RelatedArtifact struct {
	Artifact     Artifact `json:"artifact"`
	RelationType string   `json:"relation_type"`
	Confidence   float64  `json:"confidence"`
}

type RelatedResponse struct {
	Artifact Artifact          `json:"artifact"`
	Related  []RelatedArtifact `json:"related"`
}

type TagResponse struct {
	ID         string `json:"id,omitempty"`
	ArtifactID string `json:"artifact_id"`
	Tag        string `json:"tag"`
	Message    string `json:"message,omitempty"`
}

type DiscoverResponse struct {
	CrossSourceRelated int `json:"cross_source_related"`
	TagCoOccurrence    int `json:"tag_co_occurrence"`
	AuthorMatches      int `json:"author_matches"`
	CitationMatches    int `json:"citation_matches"`
	TrendingResearch   int `json:"trending_research"`
	Total              int `json:"total"`
}

type EnrichResponse struct {
	Tagged     int `json:"tagged"`
	Summarised int `json:"summarised"`
	Errors     int `json:"errors"`
}

type DigestTimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type DigestActivity struct {
	TotalIngested    int            `json:"total_ingested"`
	BySource         map[string]int `json:"by_source"`
	ByType           map[string]int `json:"by_type"`
	NewRelationships int            `json:"new_relationships"`
}

type DigestArtifact struct {
	ID           string  `json:"id"`
	Source       string  `json:"source"`
	ArtifactType string  `json:"artifact_type"`
	Title        string  `json:"title"`
	Summary      *string `json:"summary,omitempty"`
	SourceURL    *string `json:"source_url,omitempty"`
	IngestedAt   string  `json:"ingested_at"`
}

type DigestConnection struct {
	SourceTitle  string  `json:"source_title"`
	SourceType   string  `json:"source_type"`
	TargetTitle  string  `json:"target_title"`
	TargetType   string  `json:"target_type"`
	RelationType string  `json:"relation_type"`
	Confidence   float64 `json:"confidence"`
}

type DigestResponse struct {
	TimeRange       DigestTimeRange    `json:"time_range"`
	Label           string             `json:"label"`
	Narrative       string             `json:"narrative"`
	Activity        DigestActivity     `json:"activity"`
	TopArtifacts    []DigestArtifact   `json:"top_artifacts"`
	Connections     []DigestConnection `json:"connections"`
	SourceBreakdown map[string]int     `json:"source_breakdown"`
	Insights        *InsightsSummary   `json:"insights,omitempty"`
}

// --- Insight types ---

type InsightsSummary struct {
	Gems        *GemsInsight        `json:"gems,omitempty"`
	Serendipity *SerendipityInsight `json:"serendipity,omitempty"`
	Topics      *TopicsInsight      `json:"topics,omitempty"`
	Depth       *DepthInsight       `json:"depth,omitempty"`
	Velocity    *VelocityInsight    `json:"velocity,omitempty"`
	Memories    *MemoriesInsight    `json:"memories,omitempty"`
}

type GemsInsight struct {
	Count int       `json:"count"`
	Items []GemItem `json:"items"`
}

type GemItem struct {
	Title      string  `json:"title"`
	Source     string  `json:"source"`
	Similarity float64 `json:"similarity"`
	MatchedTo  string  `json:"matched_to"`
}

type SerendipityInsight struct {
	Count int              `json:"count"`
	Items []SerendipityRow `json:"items"`
}

type SerendipityRow struct {
	SourceTitle  string  `json:"source_title"`
	SourceType   string  `json:"source_type"`
	TargetTitle  string  `json:"target_title"`
	TargetType   string  `json:"target_type"`
	RelationType string  `json:"relation_type"`
	Score        float64 `json:"score"`
}

type TopicsInsight struct {
	Gaining []TopicItem `json:"gaining"`
	Cooling []TopicItem `json:"cooling"`
}

type TopicItem struct {
	Tag           string  `json:"tag"`
	ChangePercent float64 `json:"change_percent"`
}

type DepthInsight struct {
	Deep    []string `json:"deep"`
	Shallow []string `json:"shallow"`
}

type VelocityInsight struct {
	Summary string `json:"summary"`
}

type MemoriesInsight struct {
	Periods []MemoryPeriodSummary `json:"periods"`
}

type MemoryPeriodSummary struct {
	Label  string   `json:"label"`
	Count  int      `json:"count"`
	Titles []string `json:"titles"`
}

type GemsResponse struct {
	Lookback string    `json:"lookback"`
	Count    int       `json:"count"`
	Gems     []GemFull `json:"gems"`
}

type GemFull struct {
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

type SerendipityResponse struct {
	Period string            `json:"period"`
	Count  int               `json:"count"`
	Items  []SerendipityItem `json:"items"`
}

type SerendipityItem struct {
	SourceTitle  string  `json:"source_title"`
	SourceType   string  `json:"source_type"`
	TargetTitle  string  `json:"target_title"`
	TargetType   string  `json:"target_type"`
	RelationType string  `json:"relation_type"`
	Confidence   float64 `json:"confidence"`
	Score        float64 `json:"score"`
}

type TopicsResponse struct {
	Period  string       `json:"period"`
	Gaining []TopicFull  `json:"gaining"`
	Cooling []TopicFull  `json:"cooling"`
	Steady  []TopicFull  `json:"steady"`
}

type TopicFull struct {
	Tag           string  `json:"tag"`
	CurrentCount  int     `json:"current_count"`
	PreviousCount int     `json:"previous_count"`
	ChangePercent float64 `json:"change_percent"`
	Momentum      string  `json:"momentum"`
	SourceCount   int     `json:"source_count"`
}

type DepthResponse struct {
	Count   int          `json:"count"`
	Entries []DepthEntry `json:"entries"`
}

type DepthEntry struct {
	Tag            string   `json:"tag"`
	ArtifactCount  int      `json:"artifact_count"`
	SourceCount    int      `json:"source_count"`
	Sources        []string `json:"sources"`
	DepthScore     float64  `json:"depth_score"`
	Classification string   `json:"classification"`
}

type VelocityResponse struct {
	Current        VelocityPeriod `json:"current"`
	Previous       VelocityPeriod `json:"previous"`
	RollingAverage VelocityPeriod `json:"rolling_average"`
	ChangePercent  float64        `json:"change_percent"`
	Summary        string         `json:"summary"`
}

type VelocityPeriod struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Total    int            `json:"total"`
	BySource map[string]int `json:"by_source"`
	ByType   map[string]int `json:"by_type"`
}

type MemoriesResponse struct {
	Periods []MemoryPeriod `json:"periods"`
}

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
