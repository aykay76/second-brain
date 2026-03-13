package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newTrendingCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "trending",
		Short: "Show recently ingested trending repos",
		Long:  `Display trending GitHub repositories that have been ingested, sorted by most recently ingested.`,
		Example: `  pa trending
  pa trending --limit 20`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.ListArtifacts("github_trending", "", limit, "recent")
			if err != nil {
				return fmt.Errorf("fetch trending: %w", err)
			}

			header("Trending Repos")

			if resp.Count == 0 {
				fmt.Println(faint("  No trending repos ingested yet. Run: pa ingest trending"))
				return nil
			}

			for i, a := range resp.Artifacts {
				title := truncate(a.Title, 60)
				url := urlOrEmpty(a.SourceURL)
				if url != "" {
					title = hyperlink(url, title)
				}

				fmt.Printf("  %s %s\n", yellow("▲"), bold(title))

				var meta struct {
					Language  string `json:"language"`
					Stars     int    `json:"stars"`
					StarsToday int   `json:"stars_today"`
				}
				json.Unmarshal(a.Metadata, &meta)

				details := []string{}
				if meta.Language != "" {
					details = append(details, cyan(meta.Language))
				}
				if meta.Stars > 0 {
					details = append(details, yellow(fmt.Sprintf("★ %d", meta.Stars)))
				}
				if meta.StarsToday > 0 {
					details = append(details, green(fmt.Sprintf("+%d today", meta.StarsToday)))
				}

				if len(details) > 0 {
					fmt.Printf("    %s\n", joinParts(details))
				}
				if url != "" {
					fmt.Printf("    %s\n", faint(url))
				}

				if i < len(resp.Artifacts)-1 {
					fmt.Println()
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 15, "Number of repos to show")
	return cmd
}
