package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var validSources = []string{"filesystem", "github", "arxiv", "trending", "youtube", "onedrive", "thenewstack", "vision"}

func newIngestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest [source]",
		Short: "Trigger ingestion from one or all sources",
		Long: `Trigger a sync for a specific source or all configured sources.

Available sources: filesystem, github, arxiv, trending, youtube, onedrive, thenewstack, vision`,
		Example: `  pa ingest             # sync all sources
  pa ingest github      # sync only GitHub
  pa ingest vision      # start background vision ingestion job`,
		ValidArgs: append(validSources, "all"),
		Args:      cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sources := validSources
			if len(args) > 0 && args[0] != "all" {
				sources = []string{args[0]}
			}

			header("Ingestion")

			for _, source := range sources {
				if source == "vision" {
					// Handle vision ingestion asynchronously
					fmt.Printf("  %s Syncing %s...", iconSource(source), colorSource(source))

					resp, err := client.IngestVision()
					if err != nil {
						fmt.Printf(" %s\n", red(fmt.Sprintf("error: %s", err)))
					} else {
						fmt.Printf(" %s\n", green(fmt.Sprintf("started (job ID: %s)", resp.JobID)))
						fmt.Printf("    → Check status: pa ingest vision:status %s\n", resp.JobID)
					}
				} else {
					fmt.Printf("  %s Syncing %s...", iconSource(source), colorSource(source))

					resp, err := client.Ingest(source)
					if err != nil {
						fmt.Printf(" %s\n", red(fmt.Sprintf("error: %s", err)))
						continue
					}

					parts := []string{
						green(fmt.Sprintf("%d ingested", resp.Ingested)),
					}
					if resp.Skipped > 0 {
						parts = append(parts, faint(fmt.Sprintf("%d skipped", resp.Skipped)))
					}
					if resp.Errors > 0 {
						parts = append(parts, red(fmt.Sprintf("%d errors", resp.Errors)))
					}

					fmt.Printf(" %s\n", fmt.Sprintf("%s", joinParts(parts)))
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.AddCommand(newVisionStatusCmd())
	return cmd
}

// newVisionStatusCmd monitors a vision ingestion job.
func newVisionStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vision:status [job-id]",
		Short: "Monitor vision ingestion job status",
		Long:  `Check the status of a background vision ingestion job by job ID.`,
		Example: `  pa ingest vision:status abc123
  pa ingest vision:status abc123 --wait  # Wait for completion`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]

			header(fmt.Sprintf("Vision Job %s", jobID))

			for {
				status, err := client.VisionJobStatus(jobID)
				if err != nil {
					fmt.Printf("  %s\n", red(fmt.Sprintf("error checking status: %s", err)))
					return err
				}

				fmt.Printf("  Status:         %s\n", func() string {
					if status.Done {
						if status.Error != "" {
							return red("failed")
						}
						return green("completed")
					}
					return yellow("running")
				}())
				fmt.Printf("  Elapsed time:   %ds\n", status.ElapsedSeconds)

				if status.Done {
					if status.Error != "" {
						fmt.Printf("  Error:          %s\n", red(status.Error))
					} else {
						fmt.Printf("  Ingested:       %s\n", green(fmt.Sprintf("%d", status.Ingested)))
						if status.Skipped > 0 {
							fmt.Printf("  Skipped:        %d\n", status.Skipped)
						}
						if status.Errors > 0 {
							fmt.Printf("  Errors:         %s\n", red(fmt.Sprintf("%d", status.Errors)))
						}
					}
					break
				}

				// Poll every 5 seconds if not done
				time.Sleep(5 * time.Second)
			}

			fmt.Println()
			return nil
		},
	}
}

func joinParts(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}
