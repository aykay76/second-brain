package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newDepthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "depth",
		Short: "Show knowledge depth map across topics",
		Long: `Display how deeply you've covered each topic based on artifact
count multiplied by source diversity. Topics with many artifacts
from 3+ sources are classified as "deep".

Examples:
  pa depth`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Depth()
			if err != nil {
				return fmt.Errorf("fetch depth: %w", err)
			}

			if resp.Count == 0 {
				fmt.Println(faint("  No topic depth data available. Run enrichment first."))
				return nil
			}

			header("Knowledge Depth Map")

			fmt.Printf("  %-25s %-8s %-8s %-8s %s\n",
				bold("Topic"),
				bold("Depth"),
				bold("Items"),
				bold("Sources"),
				bold("Classification"),
			)
			fmt.Printf("  %s\n", faint(strings.Repeat("─", 70)))

			for _, e := range resp.Entries {
				classColor := faint
				switch e.Classification {
				case "deep":
					classColor = green
				case "moderate":
					classColor = yellow
				case "shallow":
					classColor = red
				}

				depthBar := depthVisual(e.DepthScore)
				fmt.Printf("  %-25s %s %-8d %-8d %s\n",
					truncate(e.Tag, 25),
					depthBar,
					e.ArtifactCount,
					e.SourceCount,
					classColor(e.Classification),
				)
			}
			fmt.Println()
			return nil
		},
	}

	return cmd
}

func depthVisual(score float64) string {
	n := int(score / 3)
	if n < 1 {
		n = 1
	}
	if n > 10 {
		n = 10
	}
	filled := strings.Repeat("█", n) + strings.Repeat("░", 10-n)
	return faint(fmt.Sprintf("[%s]", filled))
}
