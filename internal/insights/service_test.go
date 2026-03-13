package insights

import (
	"testing"

	"pa/internal/digest"
)

func TestConfig_WithDefaults(t *testing.T) {
	t.Parallel()

	cfg := Config{}
	resolved := cfg.withDefaults()

	if resolved.GemsLookbackDays != 90 {
		t.Errorf("GemsLookbackDays = %d, want 90", resolved.GemsLookbackDays)
	}
	if resolved.SerendipityLimit != 5 {
		t.Errorf("SerendipityLimit = %d, want 5", resolved.SerendipityLimit)
	}
	if resolved.TopicWindowWeeks != 4 {
		t.Errorf("TopicWindowWeeks = %d, want 4", resolved.TopicWindowWeeks)
	}
	if resolved.DepthMinArtifacts != 2 {
		t.Errorf("DepthMinArtifacts = %d, want 2", resolved.DepthMinArtifacts)
	}
	if resolved.VelocityRollingWeeks != 4 {
		t.Errorf("VelocityRollingWeeks = %d, want 4", resolved.VelocityRollingWeeks)
	}
	if len(resolved.MemoryLookbackMonths) != 4 {
		t.Errorf("MemoryLookbackMonths has %d items, want 4", len(resolved.MemoryLookbackMonths))
	}
	if resolved.SimilarityThreshold != 0.75 {
		t.Errorf("SimilarityThreshold = %f, want 0.75", resolved.SimilarityThreshold)
	}
}

func TestConfig_WithDefaults_PreservesCustom(t *testing.T) {
	t.Parallel()

	cfg := Config{
		GemsLookbackDays:     180,
		SerendipityLimit:     10,
		TopicWindowWeeks:     8,
		DepthMinArtifacts:    5,
		VelocityRollingWeeks: 12,
		SimilarityThreshold:  0.85,
		MemoryLookbackMonths: []int{1, 6, 12},
	}
	resolved := cfg.withDefaults()

	if resolved.GemsLookbackDays != 180 {
		t.Errorf("GemsLookbackDays = %d, want 180", resolved.GemsLookbackDays)
	}
	if resolved.SerendipityLimit != 10 {
		t.Errorf("SerendipityLimit = %d, want 10", resolved.SerendipityLimit)
	}
	if resolved.SimilarityThreshold != 0.85 {
		t.Errorf("SimilarityThreshold = %f, want 0.85", resolved.SimilarityThreshold)
	}
	if len(resolved.MemoryLookbackMonths) != 3 {
		t.Errorf("MemoryLookbackMonths has %d items, want 3", len(resolved.MemoryLookbackMonths))
	}
}

func TestDiversityMultiplier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sourceType string
		targetType string
		want       float64
	}{
		{"same source types penalised", "repo", "repo", 0.7},
		{"paper-repo boosted", "paper", "repo", 1.5},
		{"repo-paper boosted (reversed)", "repo", "paper", 1.5},
		{"paper-video boosted", "paper", "video", 1.4},
		{"trending-paper boosted", "trending", "paper", 1.4},
		{"unknown cross-source is neutral", "document", "video", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := diversityMultiplier(tt.sourceType, tt.targetType)
			if got != tt.want {
				t.Errorf("diversityMultiplier(%q, %q) = %f, want %f",
					tt.sourceType, tt.targetType, got, tt.want)
			}
		})
	}
}

func TestClassifySourceType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source string
		want   string
	}{
		{"arxiv", "paper"},
		{"springer", "paper"},
		{"github", "repo"},
		{"github_trending", "trending"},
		{"youtube", "video"},
		{"filesystem", "note"},
		{"onedrive", "document"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			t.Parallel()
			got := classifySourceType(tt.source)
			if got != tt.want {
				t.Errorf("classifySourceType(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

func TestClassifyDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		artifacts int
		sources   int
		want      string
	}{
		{"deep: many artifacts, many sources", 10, 4, "deep"},
		{"deep: threshold exact", 5, 3, "deep"},
		{"moderate: many artifacts, few sources", 5, 2, "moderate"},
		{"moderate: few artifacts, some sources", 3, 1, "moderate"},
		{"shallow: minimal", 2, 1, "shallow"},
		{"shallow: single", 1, 1, "shallow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyDepth(tt.artifacts, tt.sources)
			if got != tt.want {
				t.Errorf("classifyDepth(%d, %d) = %q, want %q",
					tt.artifacts, tt.sources, got, tt.want)
			}
		})
	}
}

func TestParsePostgresArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  int
	}{
		{"{github,arxiv,filesystem}", 3},
		{"{github}", 1},
		{"{}", 0},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := parsePostgresArray(tt.input)
			if len(got) != tt.want {
				t.Errorf("parsePostgresArray(%q) has %d items, want %d", tt.input, len(got), tt.want)
			}
		})
	}
}

func TestBuildVelocitySummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		current  int
		previous int
		avg      int
		days     int
		contains string
	}{
		{"zero both", 0, 0, 0, 7, "No artifacts"},
		{"increase", 10, 5, 7, 7, "up from 5"},
		{"decrease", 3, 8, 7, 7, "down from 8"},
		{"same", 5, 5, 5, 7, "same as last"},
		{"vs average", 14, 0, 4, 7, "vs your usual 4"},
		{"daily period", 3, 1, 2, 1, "this day"},
		{"monthly period", 50, 30, 40, 30, "this month"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildVelocitySummary(tt.current, tt.previous, tt.avg, tt.days)
			if !contains(got, tt.contains) {
				t.Errorf("buildVelocitySummary(%d, %d, %d, %d) = %q, want to contain %q",
					tt.current, tt.previous, tt.avg, tt.days, got, tt.contains)
			}
		})
	}
}

func TestAverageMap(t *testing.T) {
	t.Parallel()

	m := map[string]int{"github": 28, "arxiv": 14}
	avg := averageMap(m, 28, 7)

	if avg["github"] != 7 {
		t.Errorf("avg[github] = %d, want 7", avg["github"])
	}
	if avg["arxiv"] != 3 { // 14 * 7 / 28 = 3.5 truncated to 3
		t.Errorf("avg[arxiv] = %d, want 3", avg["arxiv"])
	}
}

func TestAverageMap_ZeroDays(t *testing.T) {
	t.Parallel()

	m := map[string]int{"github": 28}
	avg := averageMap(m, 0, 7)

	if len(avg) != 0 {
		t.Errorf("expected empty map for zero totalDays, got %v", avg)
	}
}

func TestConvertToDigestSummary_EmptyInsights(t *testing.T) {
	t.Parallel()

	all := &AllInsights{}
	summary := convertToDigestSummary(all)

	if summary.Gems != nil {
		t.Error("expected nil Gems for empty insights")
	}
	if summary.Serendipity != nil {
		t.Error("expected nil Serendipity for empty insights")
	}
	if summary.Topics != nil {
		t.Error("expected nil Topics for empty insights")
	}
	if summary.Depth != nil {
		t.Error("expected nil Depth for empty insights")
	}
	if summary.Velocity != nil {
		t.Error("expected nil Velocity for empty insights")
	}
	if summary.Memories != nil {
		t.Error("expected nil Memories for empty insights")
	}
}

func TestConvertToDigestSummary_WithData(t *testing.T) {
	t.Parallel()

	all := &AllInsights{
		Gems: &GemsResponse{
			Count: 2,
			Gems: []Gem{
				{Title: "Old Paper", Source: "arxiv", Similarity: 0.89, MatchedTo: "New Repo"},
				{Title: "Old Note", Source: "filesystem", Similarity: 0.78, MatchedTo: "New Paper"},
			},
		},
		Serendipity: &SerendipityResponse{
			Count: 1,
			Items: []SerendipityItem{
				{SourceTitle: "Paper A", SourceType: "arxiv", TargetTitle: "Repo B", TargetType: "github",
					RelationType: "IMPLEMENTS", Score: 1.3},
			},
		},
		Topics: &TopicsResponse{
			Gaining: []TopicTrend{{Tag: "RAG", ChangePercent: 50}},
			Cooling: []TopicTrend{{Tag: "Docker", ChangePercent: -30}},
		},
		Depth: &DepthResponse{
			Count: 3,
			Entries: []DepthEntry{
				{Tag: "AI", Classification: "deep"},
				{Tag: "Go", Classification: "moderate"},
				{Tag: "Rust", Classification: "shallow"},
			},
		},
		Velocity: &VelocityResponse{Summary: "14 artifacts this week, up from 4 last week."},
		Memories: &MemoriesResponse{
			Periods: []MemoryPeriod{
				{
					Label: "1 month ago", Count: 2,
					Artifacts: []MemoryArtifact{
						{Title: "K8s Migration"}, {Title: "Helm Charts"},
					},
				},
			},
		},
	}

	summary := convertToDigestSummary(all)

	if summary.Gems == nil || summary.Gems.Count != 2 {
		t.Error("expected 2 gems")
	}
	if summary.Serendipity == nil || summary.Serendipity.Count != 1 {
		t.Error("expected 1 serendipity item")
	}
	if summary.Topics == nil || len(summary.Topics.Gaining) != 1 {
		t.Error("expected 1 gaining topic")
	}
	if summary.Topics == nil || len(summary.Topics.Cooling) != 1 {
		t.Error("expected 1 cooling topic")
	}
	if summary.Depth == nil || len(summary.Depth.Deep) != 1 {
		t.Error("expected 1 deep topic")
	}
	if summary.Depth == nil || len(summary.Depth.Shallow) != 1 {
		t.Error("expected 1 shallow topic")
	}
	if summary.Velocity == nil || summary.Velocity.Summary == "" {
		t.Error("expected velocity summary")
	}
	if summary.Memories == nil || len(summary.Memories.Periods) != 1 {
		t.Error("expected 1 memory period")
	}
	if summary.Memories.Periods[0].Label != "1 month ago" {
		t.Errorf("memory label = %q, want %q", summary.Memories.Periods[0].Label, "1 month ago")
	}
}

func TestConvertToDigestSummary_GemsLimitedToFive(t *testing.T) {
	t.Parallel()

	gems := make([]Gem, 8)
	for i := range gems {
		gems[i] = Gem{Title: "gem", Source: "arxiv", Similarity: 0.9, MatchedTo: "recent"}
	}

	all := &AllInsights{
		Gems: &GemsResponse{Count: 8, Gems: gems},
	}
	summary := convertToDigestSummary(all)

	if summary.Gems.Count != 5 {
		t.Errorf("digest gems count = %d, want 5 (capped)", summary.Gems.Count)
	}
}

func TestSerendipityScoring(t *testing.T) {
	t.Parallel()

	paperRepo := SerendipityItem{
		SourceType: "arxiv", TargetType: "github",
		Confidence: 0.8,
	}
	repoRepo := SerendipityItem{
		SourceType: "github", TargetType: "github",
		Confidence: 0.9,
	}

	srcClass1 := classifySourceType(paperRepo.SourceType)
	tgtClass1 := classifySourceType(paperRepo.TargetType)
	score1 := paperRepo.Confidence * diversityMultiplier(srcClass1, tgtClass1)

	srcClass2 := classifySourceType(repoRepo.SourceType)
	tgtClass2 := classifySourceType(repoRepo.TargetType)
	score2 := repoRepo.Confidence * diversityMultiplier(srcClass2, tgtClass2)

	if score1 <= score2 {
		t.Errorf("paper→repo (%.2f) should score higher than repo→repo (%.2f)", score1, score2)
	}
}

func TestBuildNarrativeContextWithInsights(t *testing.T) {
	t.Parallel()

	ins := &digest.InsightsSummary{
		Velocity: &digest.VelocityInsight{Summary: "10 artifacts this week."},
		Topics: &digest.TopicsInsight{
			Gaining: []digest.TopicItem{{Tag: "RAG", ChangePercent: 80}},
		},
		Gems: &digest.GemsInsight{
			Count: 1,
			Items: []digest.GemItem{{Title: "Old Paper", Source: "arxiv", MatchedTo: "New Repo"}},
		},
	}

	tr := digest.TimeRange{}
	activity := digest.ActivitySummary{BySource: map[string]int{}}
	ctx := digest.BuildNarrativeContextExported(tr, activity, nil, nil, ins)

	if !contains(ctx, "Learning velocity") {
		t.Error("context should contain velocity")
	}
	if !contains(ctx, "RAG") {
		t.Error("context should contain gaining topic")
	}
	if !contains(ctx, "Old Paper") {
		t.Error("context should contain gem title")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
