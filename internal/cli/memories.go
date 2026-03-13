package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newMemoriesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memories",
		Short: "See what you were working on at this time in the past",
		Long: `Look back at artifacts from the same calendar window in previous
months and years. Rediscover what you were focused on before.

Examples:
  pa memories`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Memories()
			if err != nil {
				return fmt.Errorf("fetch memories: %w", err)
			}

			if len(resp.Periods) == 0 {
				fmt.Println(faint("  No memories found. Your knowledge base needs more history!"))
				return nil
			}

			header("Memories")

			for _, p := range resp.Periods {
				sectionHeader(fmt.Sprintf("%s (%d artifacts)", p.Label, p.Count))
				fmt.Printf("    %s\n", faint(fmt.Sprintf("%s to %s", p.From, p.To)))

				for _, a := range p.Artifacts {
					title := truncate(a.Title, 55)
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

			return nil
		},
	}

	return cmd
}
