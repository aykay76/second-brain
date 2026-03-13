package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRelatedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "related [artifact-id]",
		Short: "Show an artifact's graph neighbourhood",
		Long:  `Display an artifact and all its related artifacts with relationship types and confidence scores.`,
		Example: `  pa related a1b2c3d4-e5f6-...
  pa related a1b2c3d4     # prefix match also works if you use the full UUID`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := client.Related(args[0])
			if err != nil {
				return fmt.Errorf("fetch related: %w", err)
			}

			a := resp.Artifact
			title := a.Title
			url := urlOrEmpty(a.SourceURL)
			if url != "" {
				title = hyperlink(url, title)
			}

			header("Graph Neighbourhood")

			fmt.Printf("  %s %s\n", iconSource(a.Source), bold(title))
			fmt.Printf("    %s  %s\n\n",
				faint(fmt.Sprintf("%s/%s", a.Source, a.ArtifactType)),
				faint(a.ID[:8]),
			)

			if len(resp.Related) == 0 {
				fmt.Println(faint("  No relationships found."))
				return nil
			}

			for _, rel := range resp.Related {
				ra := rel.Artifact
				raTitle := truncate(ra.Title, 60)
				raURL := urlOrEmpty(ra.SourceURL)
				if raURL != "" {
					raTitle = hyperlink(raURL, raTitle)
				}

				confidence := ""
				if rel.Confidence > 0 {
					confidence = faint(fmt.Sprintf("(%.0f%%)", rel.Confidence*100))
				}

				fmt.Printf("  %s ─%s─▶ %s %s\n",
					formatRelType(rel.RelationType),
					faint("─"),
					iconSource(ra.Source),
					raTitle,
				)
				fmt.Printf("    %s  %s %s\n",
					faint(fmt.Sprintf("%s/%s", ra.Source, ra.ArtifactType)),
					faint(ra.ID[:8]),
					confidence,
				)
			}

			fmt.Println()
			return nil
		},
	}

	return cmd
}

func formatRelType(relType string) string {
	switch relType {
	case "RELATED_TO":
		return cyan("RELATED_TO     ")
	case "IMPLEMENTS":
		return green("IMPLEMENTS     ")
	case "SIMILAR_TOPIC":
		return yellow("SIMILAR_TOPIC  ")
	case "CITES":
		return magenta("CITES          ")
	case "REFERENCES":
		return blue("REFERENCES     ")
	case "BELONGS_TO":
		return faint("BELONGS_TO     ")
	case "LINKS_TO":
		return cyan("LINKS_TO       ")
	case "STARRED":
		return yellow("STARRED        ")
	case "AUTHORED_BY_SAME":
		return green("AUTHORED_BY_SAME")
	case "TRENDING_WITH":
		return yellow("TRENDING_WITH  ")
	default:
		return faint(fmt.Sprintf("%-16s", relType))
	}
}
