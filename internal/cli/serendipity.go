package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSerendipityCmd() *cobra.Command {
	var (
		period  string
		natural string
	)

	cmd := &cobra.Command{
		Use:   "serendipity",
		Short: "Show the most surprising cross-source connections",
		Long: `Rank cross-source relationships by how surprising they are.
A paper linked to a trending repo scores higher than two repos
from the same source.

Examples:
  pa serendipity                        # this week (default)
  pa serendipity --period monthly
  pa serendipity --natural "last 2 weeks"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Serendipity(period, natural)
			if err != nil {
				return fmt.Errorf("fetch serendipity: %w", err)
			}

			if resp.Count == 0 {
				fmt.Println(faint("  No surprising connections found this period."))
				return nil
			}

			header(fmt.Sprintf("Unexpected Connections (%s)", resp.Period))

			for i, item := range resp.Items {
				fmt.Printf("  %s %s %s\n",
					boldGreen(fmt.Sprintf("#%d", i+1)),
					truncate(item.SourceTitle, 35),
					colorSource(item.SourceType),
				)
				fmt.Printf("     %s %s %s %s\n",
					faint("↔"),
					truncate(item.TargetTitle, 35),
					colorSource(item.TargetType),
					faint(fmt.Sprintf("score: %.2f", item.Score)),
				)
				fmt.Printf("     %s %s\n",
					faint("via"),
					faint(item.RelationType),
				)
			}
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVarP(&period, "period", "p", "", "Period: daily, weekly, monthly")
	cmd.Flags().StringVarP(&natural, "natural", "n", "", `Natural language period (e.g. "last month")`)
	return cmd
}
