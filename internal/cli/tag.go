package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newTagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag [artifact-id] [tag]",
		Short: "Add a personal tag to an artifact",
		Long:  `Tag an artifact with a custom label for future filtering and discovery.`,
		Example: `  pa tag a1b2c3d4-e5f6-... "architecture"
  pa tag a1b2c3d4-e5f6-... "important"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Tag(args[0], args[1])
			if err != nil {
				return fmt.Errorf("tag failed: %w", err)
			}

			if resp.Message != "" {
				fmt.Printf("  %s %s on %s\n", yellow("●"), resp.Message, faint(resp.ArtifactID))
			} else {
				fmt.Printf("  %s Tagged %s with %s\n",
					green("✓"),
					faint(resp.ArtifactID[:8]+"..."),
					boldGreen(resp.Tag),
				)
			}

			return nil
		},
	}

	return cmd
}
