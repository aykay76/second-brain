package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newDigestCmd() *cobra.Command {
	var (
		period  string
		from    string
		to      string
		natural string
		output  string
	)

	cmd := &cobra.Command{
		Use:   "digest",
		Short: "Generate a knowledge base digest for a time period",
		Long: `Generate a summary of everything ingested, connections discovered,
and activity across your knowledge base for a given time period.

Examples:
  pa digest                             # weekly digest (default)
  pa digest --period daily
  pa digest --period monthly
  pa digest --from 2025-03-01 --to 2025-03-31
  pa digest --natural "last 2 weeks"
  pa digest --natural "in January"
  pa digest --output digest.md          # save as markdown file`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Digest(period, from, to, natural)
			if err != nil {
				return fmt.Errorf("generate digest: %w", err)
			}

			if output != "" {
				md := formatDigestMarkdown(resp)
				if err := os.WriteFile(output, []byte(md), 0644); err != nil {
					return fmt.Errorf("write file: %w", err)
				}
				fmt.Printf("%s Digest saved to %s\n", green("✓"), bold(output))
				return nil
			}

			printDigest(resp)
			return nil
		},
	}

	cmd.Flags().StringVarP(&period, "period", "p", "", "Digest period: daily, weekly, monthly (default: weekly)")
	cmd.Flags().StringVar(&from, "from", "", "Start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&to, "to", "", "End date (YYYY-MM-DD)")
	cmd.Flags().StringVarP(&natural, "natural", "n", "", `Natural language period (e.g. "last 2 weeks", "in March")`)
	cmd.Flags().StringVarP(&output, "output", "o", "", "Save digest as markdown file")

	return cmd
}

func printDigest(d *DigestResponse) {
	header(fmt.Sprintf("Knowledge Digest: %s", d.Label))

	fmt.Printf("  %s\n\n", d.Narrative)

	sectionHeader("Activity")
	fmt.Printf("    %s artifacts ingested | %s connections discovered\n\n",
		boldGreen(fmt.Sprintf("%d", d.Activity.TotalIngested)),
		boldGreen(fmt.Sprintf("%d", d.Activity.NewRelationships)),
	)

	if len(d.Activity.BySource) > 0 {
		sources := sortedKeys(d.Activity.BySource)
		for _, s := range sources {
			count := d.Activity.BySource[s]
			bar := strings.Repeat("█", barLen(count, d.Activity.TotalIngested, 30))
			fmt.Printf("    %s %-18s %s %d\n",
				iconSource(s),
				colorSource(s),
				faint(bar),
				count,
			)
		}
		fmt.Println()
	}

	if len(d.TopArtifacts) > 0 {
		sectionHeader("Recent Artifacts")
		limit := 15
		if len(d.TopArtifacts) < limit {
			limit = len(d.TopArtifacts)
		}
		for _, a := range d.TopArtifacts[:limit] {
			title := truncate(a.Title, 60)
			url := urlOrEmpty(a.SourceURL)
			if url != "" {
				title = hyperlink(url, title)
			}
			fmt.Printf("    %s %s %s\n",
				iconSource(a.Source),
				title,
				faint(a.ArtifactType),
			)
		}
		fmt.Println()
	}

	if len(d.Connections) > 0 {
		sectionHeader("Cross-Source Connections")
		for _, c := range d.Connections {
			conf := faint(fmt.Sprintf("%.0f%%", c.Confidence*100))
			fmt.Printf("    %s %s ← %s → %s %s %s\n",
				colorSource(c.SourceType),
				truncate(c.SourceTitle, 35),
				faint(c.RelationType),
				truncate(c.TargetTitle, 35),
				colorSource(c.TargetType),
				conf,
			)
		}
		fmt.Println()
	}
}

func formatDigestMarkdown(d *DigestResponse) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Knowledge Digest: %s\n\n", d.Label)
	fmt.Fprintf(&b, "%s\n\n", d.Narrative)

	writeMarkdownActivity(&b, d.Activity)
	writeMarkdownArtifacts(&b, d.TopArtifacts)
	writeMarkdownConnections(&b, d.Connections)

	return b.String()
}

func writeMarkdownActivity(b *strings.Builder, a DigestActivity) {
	fmt.Fprintf(b, "## Activity Summary\n\n")
	fmt.Fprintf(b, "**%d artifacts** ingested | **%d connections** discovered\n\n",
		a.TotalIngested, a.NewRelationships)

	if len(a.BySource) > 0 {
		b.WriteString("| Source | Count |\n|---|---|\n")
		for _, s := range sortedKeys(a.BySource) {
			fmt.Fprintf(b, "| %s | %d |\n", s, a.BySource[s])
		}
		b.WriteString("\n")
	}
}

func writeMarkdownArtifacts(b *strings.Builder, artifacts []DigestArtifact) {
	if len(artifacts) == 0 {
		return
	}
	fmt.Fprintf(b, "## Recent Artifacts\n\n")
	for _, a := range artifacts {
		fmt.Fprintf(b, "- **%s** `%s/%s`%s%s\n",
			a.Title, a.Source, a.ArtifactType,
			mdOptionalURL(a.SourceURL),
			mdOptionalSummary(a.Summary))
	}
	b.WriteString("\n")
}

func writeMarkdownConnections(b *strings.Builder, connections []DigestConnection) {
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

func mdOptionalURL(u *string) string {
	if u != nil && *u != "" {
		return fmt.Sprintf(" — [link](%s)", *u)
	}
	return ""
}

func mdOptionalSummary(s *string) string {
	if s == nil || *s == "" {
		return ""
	}
	v := *s
	if len(v) > 200 {
		v = v[:197] + "..."
	}
	return "\n  > " + v
}
