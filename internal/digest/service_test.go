package digest

import (
	"strings"
	"testing"
	"time"
)

func TestBuildNarrativeContext(t *testing.T) {
	tr := TimeRange{
		From: time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC),
	}
	activity := ActivitySummary{
		TotalIngested:    25,
		BySource:         map[string]int{"github": 10, "arxiv": 8, "filesystem": 7},
		ByType:           map[string]int{"repo": 10, "paper": 8, "document": 7},
		NewRelationships: 5,
	}

	summary := strPtr("A paper about transformers")
	artifacts := []DigestArtifact{
		{ID: "a1", Source: "arxiv", ArtifactType: "paper", Title: "Attention Is All You Need", Summary: summary},
		{ID: "a2", Source: "github", ArtifactType: "repo", Title: "transformer-go"},
	}

	connections := []DigestConnection{
		{
			SourceTitle: "Attention Is All You Need", SourceType: "arxiv",
			TargetTitle: "transformer-go", TargetType: "github",
			RelationType: "IMPLEMENTS", Confidence: 0.92,
		},
	}

	ctx := buildNarrativeContext(tr, activity, artifacts, connections, nil)

	if !strings.Contains(ctx, "7 Mar 2026") {
		t.Error("context should contain date range label")
	}
	if !strings.Contains(ctx, "25") {
		t.Error("context should contain total count")
	}
	if !strings.Contains(ctx, "github: 10") {
		t.Error("context should contain source breakdown")
	}
	if !strings.Contains(ctx, "Attention Is All You Need") {
		t.Error("context should contain artifact titles")
	}
	if !strings.Contains(ctx, "IMPLEMENTS") {
		t.Error("context should contain connection types")
	}
	if !strings.Contains(ctx, "transformer-go") {
		t.Error("context should contain connection targets")
	}
}

func TestFallbackNarrative_Empty(t *testing.T) {
	svc := &Service{cfg: Config{DefaultPeriod: PeriodWeekly}}
	tr := TimeRange{
		From: time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC),
	}
	activity := ActivitySummary{TotalIngested: 0, BySource: map[string]int{}}

	got := svc.fallbackNarrative(tr, activity)
	if !strings.Contains(got, "No new artifacts") {
		t.Errorf("fallback = %q, should mention no artifacts", got)
	}
}

func TestFallbackNarrative_WithData(t *testing.T) {
	svc := &Service{cfg: Config{DefaultPeriod: PeriodWeekly}}
	tr := TimeRange{
		From: time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC),
	}
	activity := ActivitySummary{
		TotalIngested:    15,
		BySource:         map[string]int{"github": 10, "arxiv": 5},
		NewRelationships: 3,
	}

	got := svc.fallbackNarrative(tr, activity)
	if !strings.Contains(got, "15 artifacts") {
		t.Errorf("fallback should mention 15 artifacts, got %q", got)
	}
	if !strings.Contains(got, "github") {
		t.Errorf("fallback should mention github, got %q", got)
	}
	if !strings.Contains(got, "3 new connections") {
		t.Errorf("fallback should mention connections, got %q", got)
	}
}

func TestFormatMarkdown(t *testing.T) {
	url := "https://github.com/example/repo"
	summary := "An implementation of attention mechanisms"
	d := &DigestResponse{
		TimeRange: TimeRange{
			From: time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC),
		},
		Label:     "7 Mar 2026 – 13 Mar 2026",
		Narrative: "This was a busy week focused on transformers research.",
		Activity: ActivitySummary{
			TotalIngested:    20,
			BySource:         map[string]int{"github": 12, "arxiv": 8},
			NewRelationships: 4,
		},
		TopArtifacts: []DigestArtifact{
			{
				ID: "a1", Source: "arxiv", ArtifactType: "paper",
				Title: "Attention Is All You Need", Summary: &summary,
				IngestedAt: "2026-03-10",
			},
			{
				ID: "a2", Source: "github", ArtifactType: "repo",
				Title: "transformer-go", SourceURL: &url,
				IngestedAt: "2026-03-11",
			},
		},
		Connections: []DigestConnection{
			{
				SourceTitle: "Attention Is All You Need", SourceType: "arxiv",
				TargetTitle: "transformer-go", TargetType: "github",
				RelationType: "IMPLEMENTS", Confidence: 0.92,
			},
		},
	}

	md := FormatMarkdown(d)

	checks := []string{
		"# Knowledge Digest:",
		"busy week",
		"## Activity Summary",
		"**20 artifacts**",
		"**4 connections**",
		"github", "arxiv",
		"## Recent Artifacts",
		"Attention Is All You Need",
		"transformer-go",
		"[link](https://github.com/example/repo)",
		"## Cross-Source Connections",
		"IMPLEMENTS",
		"92%",
	}

	for _, check := range checks {
		if !strings.Contains(md, check) {
			t.Errorf("markdown should contain %q", check)
		}
	}
}

func TestResolveTimeRange_Period(t *testing.T) {
	svc := &Service{cfg: Config{DefaultPeriod: PeriodWeekly}}
	req := DigestRequest{
		Period: PeriodDaily,
		Now:    refTime,
	}

	tr, err := svc.resolveTimeRange(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertDate(t, tr.From, "2026-03-13")
	assertDate(t, tr.To, "2026-03-14")
}

func TestResolveTimeRange_FromTo(t *testing.T) {
	svc := &Service{cfg: Config{DefaultPeriod: PeriodWeekly}}
	from := "2025-03-01"
	to := "2025-03-31"
	req := DigestRequest{
		From: &from,
		To:   &to,
		Now:  refTime,
	}

	tr, err := svc.resolveTimeRange(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertDate(t, tr.From, "2025-03-01")
	assertDate(t, tr.To, "2025-03-31")
}

func TestResolveTimeRange_Natural(t *testing.T) {
	svc := &Service{cfg: Config{DefaultPeriod: PeriodWeekly}}
	req := DigestRequest{
		NaturalTZ: "last 2 weeks",
		Now:       refTime,
	}

	tr, err := svc.resolveTimeRange(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertDate(t, tr.From, "2026-02-27")
	assertDate(t, tr.To, "2026-03-14")
}

func TestResolveTimeRange_DefaultPeriod(t *testing.T) {
	svc := &Service{cfg: Config{DefaultPeriod: PeriodMonthly}}
	req := DigestRequest{Now: refTime}

	tr, err := svc.resolveTimeRange(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertDate(t, tr.From, "2026-02-13")
	assertDate(t, tr.To, "2026-03-14")
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input  string
		max    int
		expect string
	}{
		{"short", 10, "short"},
		{"this is a long string here", 15, "this is a lo..."},
		{"exact", 5, "exact"},
	}

	for _, tt := range tests {
		got := truncateStr(tt.input, tt.max)
		if got != tt.expect {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expect)
		}
	}
}

func TestSortedSourceKeys(t *testing.T) {
	m := map[string]int{"arxiv": 5, "github": 20, "filesystem": 3}
	keys := sortedSourceKeys(m)

	if len(keys) != 3 {
		t.Fatalf("got %d keys, want 3", len(keys))
	}
	if keys[0] != "github" {
		t.Errorf("keys[0] = %q, want %q", keys[0], "github")
	}
	if keys[1] != "arxiv" {
		t.Errorf("keys[1] = %q, want %q", keys[1], "arxiv")
	}
}

func strPtr(s string) *string { return &s }
