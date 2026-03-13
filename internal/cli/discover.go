package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDiscoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Run cross-source relationship discovery",
		Long:  `Trigger the discovery engine to find relationships between artifacts across all sources.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("  %s Running discovery engine...\n", faint("●"))

			resp, err := client.Discover()
			if err != nil {
				return fmt.Errorf("discovery failed: %w", err)
			}

			header("Discovery Results")

			if resp.Total == 0 {
				fmt.Println(faint("  No new relationships discovered."))
				return nil
			}

			if resp.CrossSourceRelated > 0 {
				fmt.Printf("  %s Cross-source related:  %s\n", cyan("●"), boldGreen(fmt.Sprintf("%d", resp.CrossSourceRelated)))
			}
			if resp.TagCoOccurrence > 0 {
				fmt.Printf("  %s Tag co-occurrence:     %s\n", yellow("●"), boldGreen(fmt.Sprintf("%d", resp.TagCoOccurrence)))
			}
			if resp.AuthorMatches > 0 {
				fmt.Printf("  %s Author matches:        %s\n", green("●"), boldGreen(fmt.Sprintf("%d", resp.AuthorMatches)))
			}
			if resp.CitationMatches > 0 {
				fmt.Printf("  %s Citation matches:      %s\n", magenta("●"), boldGreen(fmt.Sprintf("%d", resp.CitationMatches)))
			}
			if resp.TrendingResearch > 0 {
				fmt.Printf("  %s Trending ↔ research:   %s\n", yellow("●"), boldGreen(fmt.Sprintf("%d", resp.TrendingResearch)))
			}

			fmt.Printf("\n  Total new relationships: %s\n\n", bold(fmt.Sprintf("%d", resp.Total)))

			return nil
		},
	}

	return cmd
}
