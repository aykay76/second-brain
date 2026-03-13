package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show knowledge base status and statistics",
		Long:  `Display artifact counts, embedding coverage, relationship stats, and sync cursor timestamps.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			health, err := client.Health()
			if err != nil {
				return fmt.Errorf("server unreachable: %w", err)
			}

			resp, err := client.Status()
			if err != nil {
				return fmt.Errorf("fetch status: %w", err)
			}

			header("Knowledge Base Status")

			dbStatus := green("●")
			if health.Database != "up" {
				dbStatus = red("●")
			}
			fmt.Printf("  %s Server %s  Database %s %s\n\n",
				dbStatus,
				green(serverURL),
				dbStatus,
				health.Database,
			)

			sectionHeader("Artifacts")
			fmt.Printf("    Total: %s\n", boldGreen(fmt.Sprintf("%d", resp.Artifacts.Total)))

			sources := sortedKeys(resp.Artifacts.BySource)
			for _, s := range sources {
				count := resp.Artifacts.BySource[s]
				bar := strings.Repeat("█", barLen(count, resp.Artifacts.Total, 30))
				fmt.Printf("    %s %-18s %s %d\n",
					iconSource(s),
					colorSource(s),
					faint(bar),
					count,
				)
			}

			fmt.Println()
			sectionHeader("Types")
			types := sortedKeys(resp.Artifacts.ByType)
			for _, t := range types {
				count := resp.Artifacts.ByType[t]
				fmt.Printf("    %-18s %d\n", faint(t), count)
			}

			fmt.Println()
			sectionHeader("Embeddings")
			coverageColor := green
			if resp.Embeddings.Coverage < 80 {
				coverageColor = yellow
			}
			if resp.Embeddings.Coverage < 50 {
				coverageColor = red
			}
			fmt.Printf("    %d embedded of %d artifacts (%s)\n",
				resp.Embeddings.Total,
				resp.Artifacts.Total,
				coverageColor(fmt.Sprintf("%.1f%% coverage", resp.Embeddings.Coverage)),
			)

			if resp.Relationships.Total > 0 {
				fmt.Println()
				sectionHeader("Relationships")
				fmt.Printf("    Total: %s\n", boldGreen(fmt.Sprintf("%d", resp.Relationships.Total)))
				relTypes := sortedKeys(resp.Relationships.ByType)
				for _, t := range relTypes {
					count := resp.Relationships.ByType[t]
					fmt.Printf("    %-22s %d\n", faint(t), count)
				}
			}

			if len(resp.SyncCursors) > 0 {
				fmt.Println()
				sectionHeader("Last Sync")
				for _, sc := range resp.SyncCursors {
					ago := formatTimeAgo(sc.UpdatedAt)
					fmt.Printf("    %-30s %s\n", faint(sc.SourceName), ago)
				}
			}

			fmt.Println()
			return nil
		},
	}

	return cmd
}

func barLen(value, total, maxLen int) int {
	if total == 0 {
		return 0
	}
	n := value * maxLen / total
	if n < 1 && value > 0 {
		n = 1
	}
	return n
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return m[keys[i]] > m[keys[j]]
	})
	return keys
}

func formatTimeAgo(ts string) string {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999-07",
		"2006-01-02 15:04:05.999999+00",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
	}

	var t time.Time
	var err error
	for _, f := range formats {
		t, err = time.Parse(f, ts)
		if err == nil {
			break
		}
	}
	if err != nil {
		return faint(ts)
	}

	d := time.Since(t)
	switch {
	case d < time.Minute:
		return green("just now")
	case d < time.Hour:
		return green(fmt.Sprintf("%dm ago", int(d.Minutes())))
	case d < 24*time.Hour:
		return yellow(fmt.Sprintf("%dh ago", int(d.Hours())))
	default:
		return faint(fmt.Sprintf("%dd ago", int(d.Hours()/24)))
	}
}
