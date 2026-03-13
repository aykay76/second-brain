package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newTopicsCmd() *cobra.Command {
	var (
		trending bool
		weeks    int
	)

	cmd := &cobra.Command{
		Use:   "topics",
		Short: "Show topic momentum and drift in your knowledge base",
		Long: `Analyse which topics are gaining or losing momentum based on
tag frequency changes between the current and previous period.

Examples:
  pa topics                # all topics with momentum
  pa topics --trending     # only gaining topics
  pa topics --weeks 8      # compare over 8-week windows`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Topics(weeks)
			if err != nil {
				return fmt.Errorf("fetch topics: %w", err)
			}

			header(fmt.Sprintf("Topic Momentum (%s)", resp.Period))

			if len(resp.Gaining) > 0 {
				sectionHeader("Gaining Momentum")
				for _, t := range resp.Gaining {
					bar := momentumBar(t.ChangePercent)
					fmt.Printf("    %-25s %s %s  %s\n",
						bold(t.Tag),
						green(fmt.Sprintf("+%.0f%%", t.ChangePercent)),
						bar,
						faint(fmt.Sprintf("%d→%d across %d sources", t.PreviousCount, t.CurrentCount, t.SourceCount)),
					)
				}
				fmt.Println()
			}

			if !trending && len(resp.Cooling) > 0 {
				sectionHeader("Cooling Off")
				for _, t := range resp.Cooling {
					bar := momentumBar(t.ChangePercent)
					fmt.Printf("    %-25s %s %s  %s\n",
						bold(t.Tag),
						red(fmt.Sprintf("%.0f%%", t.ChangePercent)),
						bar,
						faint(fmt.Sprintf("%d→%d across %d sources", t.PreviousCount, t.CurrentCount, t.SourceCount)),
					)
				}
				fmt.Println()
			}

			if !trending && len(resp.Steady) > 0 {
				sectionHeader("Steady")
				limit := 10
				if len(resp.Steady) < limit {
					limit = len(resp.Steady)
				}
				for _, t := range resp.Steady[:limit] {
					fmt.Printf("    %-25s %s\n",
						faint(t.Tag),
						faint(fmt.Sprintf("%d artifacts, %d sources", t.CurrentCount, t.SourceCount)),
					)
				}
				fmt.Println()
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&trending, "trending", false, "Show only gaining topics")
	cmd.Flags().IntVar(&weeks, "weeks", 0, "Comparison window in weeks (default: 4)")
	return cmd
}

func momentumBar(pct float64) string {
	if pct > 0 {
		n := int(pct / 10)
		if n < 1 {
			n = 1
		}
		if n > 10 {
			n = 10
		}
		return green(strings.Repeat("▲", n))
	}
	if pct < 0 {
		n := int(-pct / 10)
		if n < 1 {
			n = 1
		}
		if n > 10 {
			n = 10
		}
		return red(strings.Repeat("▼", n))
	}
	return faint("—")
}
