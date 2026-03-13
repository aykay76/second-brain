package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newAskCmd() *cobra.Command {
	var topK int

	cmd := &cobra.Command{
		Use:   "ask [question]",
		Short: "Ask a question and get a grounded answer from your knowledge base",
		Long: `Ask a question using the RAG pipeline. Your question is embedded,
matched against your knowledge base via hybrid search, and answered
by the LLM with citations to source material.`,
		Example: `  pa ask "what's new in RAG research?"
  pa ask "what CLI tool did I star for working with JSON?"
  pa ask --top-k 20 "how does event sourcing work?"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			question := strings.Join(args, " ")

			fmt.Printf("%s %s\n\n", faint("Asking:"), bold(question))

			resp, err := client.Ask(question, topK)
			if err != nil {
				return fmt.Errorf("ask failed: %w", err)
			}

			fmt.Println(resp.Answer)

			cited := 0
			for _, s := range resp.Sources {
				if s.Cited {
					cited++
				}
			}

			if cited > 0 {
				fmt.Printf("\n%s\n", bold("Sources:"))
				for _, s := range resp.Sources {
					if !s.Cited {
						continue
					}
					url := urlOrEmpty(s.SourceURL)
					title := truncate(s.Title, 60)
					if url != "" {
						title = hyperlink(url, title)
					}
					fmt.Printf("  %s [%d] %s %s %s\n",
						iconSource(s.Source),
						s.Index,
						title,
						faint(fmt.Sprintf("(%s/%s)", s.Source, s.ArtifactType)),
						faint(fmt.Sprintf("%.0f%%", s.Score*100)),
					)
				}
			}

			if len(resp.Sources) > cited {
				fmt.Printf("\n%s\n", faint(fmt.Sprintf("  + %d uncited sources considered", len(resp.Sources)-cited)))
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&topK, "top-k", 0, "Number of sources to retrieve (default: server decides)")
	return cmd
}
