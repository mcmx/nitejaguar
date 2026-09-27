// Command nitejaguar is the client-only binary.
//
// It shares all framework and contract code (common, internal/workflow
// types, internal/actions) with the server binary, but never links the
// server stack (internal/server, internal/database, ent, echo/huma, templ).
// Build with:
//
//	go build -o nitejaguar ./cmd/client
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	njclient "github.com/mcmx/nitejaguar/internal/client"
	"github.com/mcmx/nitejaguar/internal/version"
	"github.com/mcmx/nitejaguar/internal/workflow"
	"github.com/spf13/cobra"
)

var (
	clientServer          string
	clientID              string
	clientName            string
	clientToken           string
	clientEnrollmentToken string
)

func runClient(cmd *cobra.Command, _ []string) error {
	return njclient.Run(cmd.Context(), njclient.Config{
		Server: clientServer, ClientID: clientID, Name: clientName, Token: clientToken,
		EnrollmentToken: clientEnrollmentToken,
	}, nil)
}

func runClientWorkflowFileOp(path string, clone bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read workflow file %q: %w", path, err)
	}
	var wf workflow.Workflow
	if err := json.Unmarshal(raw, &wf); err != nil {
		return fmt.Errorf("invalid workflow JSON in %q: %w", path, err)
	}

	api := njclient.API{BaseURL: clientServer, Token: clientToken}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	op := "import"
	var res njclient.UpsertWorkflowResponse
	if clone {
		op = "clone"
		res, err = api.CloneWorkflow(ctx, wf)
	} else {
		res, err = api.ImportWorkflow(ctx, wf)
	}
	if err != nil {
		return fmt.Errorf("%s workflow %q via server %s: %w", op, path, clientServer, err)
	}
	fmt.Printf("%s of %q succeeded (workflow_id=%s)\n", op, path, res.WorkflowID)
	return nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	rootCmd := &cobra.Command{
		Use:     "nitejaguar",
		Short:   "NiteJaguar client",
		Long:    `NiteJaguar client: polls the server for assignments and executes triggers and actions.`,
		Version: version.Version,
		RunE:    runClient,
	}

	rootCmd.PersistentFlags().StringVar(&clientServer, "server", envOr("NITEJAGUAR_SERVER", "http://127.0.0.1:8080"), "NiteJaguar server URL")
	rootCmd.PersistentFlags().StringVar(&clientID, "client-id", os.Getenv("NITEJAGUAR_CLIENT_ID"), "existing registered client ID")
	rootCmd.PersistentFlags().StringVar(&clientName, "name", envOr("NITEJAGUAR_CLIENT_NAME", "nitejaguar-client"), "client name used during registration")
	rootCmd.PersistentFlags().StringVar(&clientToken, "token", os.Getenv("NITEJAGUAR_TOKEN"), "API token")
	rootCmd.PersistentFlags().StringVar(&clientEnrollmentToken, "enrollment-token", os.Getenv("NITEJAGUAR_ENROLLMENT_TOKEN"), "tenant enrollment/join token for self-registration")

	// `nitejaguar client` stays working as an alias for the root command so
	// scripts written against the old monolith keep functioning.
	clientCmd := &cobra.Command{
		Use:   "client",
		Short: "Start NiteJaguar in client mode (alias for root command)",
		RunE:  runClient,
	}
	rootCmd.AddCommand(clientCmd)

	workflowCmd := &cobra.Command{
		Use:   "workflow",
		Short: "Manage workflows via the server API",
		Long:  `Import or clone workflow JSON files through a running Nitejaguar server. The server saves them to its database; this command never starts a server or client runner itself.`,
	}
	workflowImportCmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import a workflow JSON file verbatim (upsert)",
		Long:  `Reads a workflow JSON file and POSTs it to the server, which saves it verbatim, keeping ids and name. Overwrites a workflow with the same id. The server must be running.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runClientWorkflowFileOp(args[0], false)
		},
	}
	workflowCloneCmd := &cobra.Command{
		Use:   "clone <file>",
		Short: "Clone a workflow JSON file with fresh ids",
		Long:  `Reads a workflow JSON file and POSTs it to the server, which saves an independent copy with fresh workflow_/trigger_/action_ ids, rewritten edges, and a "Clone of: " name prefix. The server must be running.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runClientWorkflowFileOp(args[0], true)
		},
	}
	rootCmd.AddCommand(workflowCmd)
	workflowCmd.AddCommand(workflowImportCmd)
	workflowCmd.AddCommand(workflowCloneCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
