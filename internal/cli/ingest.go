package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var validSources = []string{"filesystem", "github", "arxiv", "trending", "youtube", "onedrive"}

func newIngestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest [source]",
		Short: "Trigger ingestion from one or all sources",
		Long: `Trigger a sync for a specific source or all configured sources.

Available sources: filesystem, github, arxiv, trending, youtube, onedrive`,
		Example: `  pa ingest             # sync all sources
  pa ingest github      # sync only GitHub
  pa ingest arxiv       # sync only arXiv papers`,
		ValidArgs: append(validSources, "all"),
		Args:      cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sources := validSources
			if len(args) > 0 && args[0] != "all" {
				sources = []string{args[0]}
			}

			header("Ingestion")

			for _, source := range sources {
				fmt.Printf("  %s Syncing %s...", iconSource(source), colorSource(source))

				resp, err := client.Ingest(source)
				if err != nil {
					fmt.Printf(" %s\n", red(fmt.Sprintf("error: %s", err)))
					continue
				}

				parts := []string{
					green(fmt.Sprintf("%d ingested", resp.Ingested)),
				}
				if resp.Skipped > 0 {
					parts = append(parts, faint(fmt.Sprintf("%d skipped", resp.Skipped)))
				}
				if resp.Errors > 0 {
					parts = append(parts, red(fmt.Sprintf("%d errors", resp.Errors)))
				}

				fmt.Printf(" %s\n", fmt.Sprintf("%s", joinParts(parts)))
			}

			fmt.Println()
			return nil
		},
	}

	return cmd
}

func joinParts(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}
