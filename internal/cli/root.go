package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	serverURL string
	client    *Client
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "pa",
		Short: "Personal AI Memory Agent",
		Long: `pa — your personal knowledge recall and discovery tool.

Query your knowledge base, trigger ingestion, explore relationships,
and track research trends — all from the terminal.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			client = NewClient(serverURL)
		},
	}

	root.PersistentFlags().StringVar(&serverURL, "server", defaultServerURL(), "PA server URL")

	root.AddCommand(
		newAskCmd(),
		newSearchCmd(),
		newIngestCmd(),
		newTrendingCmd(),
		newPapersCmd(),
		newRelatedCmd(),
		newStatusCmd(),
		newTagCmd(),
		newDiscoverCmd(),
	)

	return root
}

func defaultServerURL() string {
	if u := os.Getenv("PA_SERVER_URL"); u != "" {
		return u
	}
	return "http://localhost:8080"
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, red(err))
		os.Exit(1)
	}
}
