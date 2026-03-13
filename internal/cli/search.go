package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	var (
		limit    int
		semantic bool
		tags     []string
	)

	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search your knowledge base",
		Long:  `Perform a hybrid (semantic + full-text) search across all ingested artifacts.`,
		Example: `  pa search "event sourcing"
  pa search --limit 5 "kubernetes operators"
  pa search --semantic "distributed consensus algorithms"
  pa search --tags architecture,database "migration patterns"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			mode := ""
			if semantic {
				mode = "semantic"
			}

			resp, err := client.SearchWithTags(query, limit, mode, tags)
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}

			modeLabel := "hybrid"
			if semantic {
				modeLabel = "semantic"
			}
			extra := ""
			if len(tags) > 0 {
				extra = fmt.Sprintf(", tags: %s", strings.Join(tags, ","))
			}
			fmt.Printf("%s %s %s\n\n",
				faint("Search:"), bold(resp.Query),
				faint(fmt.Sprintf("(%d results, %s%s)", resp.Count, modeLabel, extra)),
			)

			if resp.Count == 0 {
				fmt.Println(faint("  No results found."))
				return nil
			}

			for i, r := range resp.Results {
				title := truncate(r.Title, 70)
				url := urlOrEmpty(r.SourceURL)
				if url != "" {
					title = hyperlink(url, title)
				}

				fmt.Printf("  %s %s %s\n",
					iconSource(r.Source),
					title,
					scoreBar(r.Score),
				)
				fmt.Printf("    %s  %s\n",
					faint(fmt.Sprintf("%s/%s", r.Source, r.ArtifactType)),
					faint(r.ID[:8]),
				)

				if r.Summary != nil && *r.Summary != "" {
					fmt.Printf("    %s\n", faint(truncate(*r.Summary, 100)))
				}

				if url != "" {
					fmt.Printf("    %s\n", cyan(url))
				}

				if i < len(resp.Results)-1 {
					fmt.Println()
				}
			}

			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "Maximum number of results")
	cmd.Flags().BoolVar(&semantic, "semantic", false, "Use semantic-only search (no full-text)")
	cmd.Flags().StringSliceVar(&tags, "tags", nil, "Filter results by tags (comma-separated)")
	return cmd
}
