package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newPapersCmd() *cobra.Command {
	var (
		limit  int
		source string
	)

	cmd := &cobra.Command{
		Use:   "papers",
		Short: "Show recently ingested research papers",
		Long:  `Display arXiv and Springer papers from your knowledge base, sorted by most recently ingested.`,
		Example: `  pa papers
  pa papers --limit 20
  pa papers --source arxiv`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" {
				source = "arxiv"
			}

			resp, err := client.ListArtifacts(source, "paper", limit, "recent")
			if err != nil {
				return fmt.Errorf("fetch papers: %w", err)
			}

			header(fmt.Sprintf("Recent Papers (%s)", source))

			if resp.Count == 0 {
				fmt.Println(faint(fmt.Sprintf("  No papers ingested yet. Run: pa ingest %s", source)))
				return nil
			}

			for i, a := range resp.Artifacts {
				title := truncate(a.Title, 70)
				url := urlOrEmpty(a.SourceURL)
				if url != "" {
					title = hyperlink(url, title)
				}

				fmt.Printf("  %s %s\n", magenta("◇"), bold(title))

				var meta struct {
					Authors    []string `json:"authors"`
					Categories []string `json:"categories"`
					ArxivID    string   `json:"arxiv_id"`
					DOI        string   `json:"doi"`
				}
				json.Unmarshal(a.Metadata, &meta)

				if len(meta.Authors) > 0 {
					authors := strings.Join(meta.Authors, ", ")
					if len(authors) > 80 {
						authors = authors[:77] + "..."
					}
					fmt.Printf("    %s\n", faint(authors))
				}

				details := []string{}
				if meta.ArxivID != "" {
					details = append(details, cyan(meta.ArxivID))
				}
				if meta.DOI != "" {
					details = append(details, cyan(meta.DOI))
				}
				if len(meta.Categories) > 0 {
					details = append(details, faint(strings.Join(meta.Categories, ", ")))
				}

				if len(details) > 0 {
					fmt.Printf("    %s\n", joinParts(details))
				}

				if url != "" {
					fmt.Printf("    %s\n", faint(url))
				}

				if i < len(resp.Artifacts)-1 {
					fmt.Println()
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 15, "Number of papers to show")
	cmd.Flags().StringVarP(&source, "source", "s", "arxiv", "Paper source (arxiv, springer)")
	return cmd
}
