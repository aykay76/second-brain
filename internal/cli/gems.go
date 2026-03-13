package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newGemsCmd() *cobra.Command {
	var lookback int

	cmd := &cobra.Command{
		Use:   "gems",
		Short: "Resurface forgotten artifacts similar to recent activity",
		Long: `Find older artifacts in your knowledge base that are semantically
similar to what you've been ingesting recently — things you might
want to revisit.

Examples:
  pa gems                  # default 90-day lookback
  pa gems --lookback 180   # search further back`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Gems(lookback)
			if err != nil {
				return fmt.Errorf("fetch gems: %w", err)
			}

			if resp.Count == 0 {
				fmt.Println(faint("  No forgotten gems found. Keep ingesting!"))
				return nil
			}

			header(fmt.Sprintf("Forgotten Gems (lookback: %s)", resp.Lookback))

			for _, g := range resp.Gems {
				title := truncate(g.Title, 55)
				url := urlOrEmpty(g.SourceURL)
				if url != "" {
					title = hyperlink(url, title)
				}
				fmt.Printf("  %s %s %s\n",
					iconSource(g.Source),
					title,
					faint(fmt.Sprintf("%.0f%% match", g.Similarity*100)),
				)
				fmt.Printf("    %s similar to: %s\n",
					faint("↳"),
					cyan(truncate(g.MatchedTo, 50)),
				)
			}
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().IntVar(&lookback, "lookback", 0, "Lookback period in days (default: 90)")
	return cmd
}
