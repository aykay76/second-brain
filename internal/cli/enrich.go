package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newEnrichCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enrich",
		Short: "Auto-tag and summarise artifacts",
		Long:  `Trigger auto-tagging and summary generation for artifacts that are missing tags or summaries.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s Running enrichment (auto-tagging + summaries)...\n\n", faint("⏳"))

			resp, err := client.Enrich()
			if err != nil {
				return fmt.Errorf("enrichment failed: %w", err)
			}

			header("Enrichment Complete")
			keyValue("Auto-tagged:", resp.Tagged)
			keyValue("Summarised:", resp.Summarised)
			if resp.Errors > 0 {
				keyValue("Errors:", yellow(resp.Errors))
			}
			fmt.Println()

			return nil
		},
	}
}
