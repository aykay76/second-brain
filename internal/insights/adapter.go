package insights

import (
	"context"

	"pa/internal/digest"
)

// GatherForDigest implements digest.InsightProvider by collecting all insights
// and converting them into the digest-native InsightsSummary format.
func (s *Service) GatherForDigest(ctx context.Context, tr digest.TimeRange) *digest.InsightsSummary {
	all := s.gatherAll(ctx, tr)
	return convertToDigestSummary(all)
}

func (s *Service) gatherAll(ctx context.Context, tr digest.TimeRange) *AllInsights {
	return s.GatherForDigestRaw(ctx, tr)
}

// GatherForDigestRaw is an alias to the full insights gather for internal use.
func (s *Service) GatherForDigestRaw(ctx context.Context, tr digest.TimeRange) *AllInsights {
	return s.GatherAllInsights(ctx, tr)
}

// GatherAllInsights collects all insight sections (exported for digest integration).
func (s *Service) GatherAllInsights(ctx context.Context, tr digest.TimeRange) *AllInsights {
	all := &AllInsights{}

	if gems, err := s.ForgottenGems(ctx, 0); err == nil && gems.Count > 0 {
		all.Gems = gems
	}

	if ser, err := s.Serendipity(ctx, tr); err == nil && ser.Count > 0 {
		all.Serendipity = ser
	}

	if topics, err := s.TopicMomentum(ctx, 0); err == nil && (len(topics.Gaining) > 0 || len(topics.Cooling) > 0) {
		all.Topics = topics
	}

	if depth, err := s.KnowledgeDepth(ctx); err == nil && depth.Count > 0 {
		all.Depth = depth
	}

	if vel, err := s.LearningVelocity(ctx, tr); err == nil {
		all.Velocity = vel
	}

	if mem, err := s.Memories(ctx, nil); err == nil && len(mem.Periods) > 0 {
		all.Memories = mem
	}

	return all
}

func convertToDigestSummary(all *AllInsights) *digest.InsightsSummary {
	summary := &digest.InsightsSummary{}

	if all.Gems != nil && all.Gems.Count > 0 {
		items := make([]digest.GemItem, 0, len(all.Gems.Gems))
		limit := 5
		if len(all.Gems.Gems) < limit {
			limit = len(all.Gems.Gems)
		}
		for _, g := range all.Gems.Gems[:limit] {
			items = append(items, digest.GemItem{
				Title:      g.Title,
				Source:     g.Source,
				Similarity: g.Similarity,
				MatchedTo:  g.MatchedTo,
			})
		}
		summary.Gems = &digest.GemsInsight{Count: len(items), Items: items}
	}

	if all.Serendipity != nil && all.Serendipity.Count > 0 {
		items := make([]digest.SerendipityRow, 0, len(all.Serendipity.Items))
		for _, s := range all.Serendipity.Items {
			items = append(items, digest.SerendipityRow{
				SourceTitle:  s.SourceTitle,
				SourceType:   s.SourceType,
				TargetTitle:  s.TargetTitle,
				TargetType:   s.TargetType,
				RelationType: s.RelationType,
				Score:        s.Score,
			})
		}
		summary.Serendipity = &digest.SerendipityInsight{Count: len(items), Items: items}
	}

	if all.Topics != nil {
		topics := &digest.TopicsInsight{}
		for _, t := range all.Topics.Gaining {
			topics.Gaining = append(topics.Gaining, digest.TopicItem{
				Tag: t.Tag, ChangePercent: t.ChangePercent,
			})
		}
		for _, t := range all.Topics.Cooling {
			topics.Cooling = append(topics.Cooling, digest.TopicItem{
				Tag: t.Tag, ChangePercent: t.ChangePercent,
			})
		}
		if len(topics.Gaining) > 0 || len(topics.Cooling) > 0 {
			summary.Topics = topics
		}
	}

	if all.Depth != nil && all.Depth.Count > 0 {
		depth := &digest.DepthInsight{}
		for _, e := range all.Depth.Entries {
			switch e.Classification {
			case "deep":
				depth.Deep = append(depth.Deep, e.Tag)
			case "shallow":
				depth.Shallow = append(depth.Shallow, e.Tag)
			}
		}
		if len(depth.Deep) > 0 || len(depth.Shallow) > 0 {
			summary.Depth = depth
		}
	}

	if all.Velocity != nil && all.Velocity.Summary != "" {
		summary.Velocity = &digest.VelocityInsight{Summary: all.Velocity.Summary}
	}

	if all.Memories != nil && len(all.Memories.Periods) > 0 {
		mem := &digest.MemoriesInsight{}
		for _, p := range all.Memories.Periods {
			titles := make([]string, 0, len(p.Artifacts))
			limit := 3
			if len(p.Artifacts) < limit {
				limit = len(p.Artifacts)
			}
			for _, a := range p.Artifacts[:limit] {
				titles = append(titles, a.Title)
			}
			mem.Periods = append(mem.Periods, digest.MemoryPeriodSummary{
				Label:  p.Label,
				Count:  p.Count,
				Titles: titles,
			})
		}
		summary.Memories = mem
	}

	return summary
}
