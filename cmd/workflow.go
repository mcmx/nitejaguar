package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	njclient "github.com/mcmx/nitejaguar/internal/client"
	"github.com/mcmx/nitejaguar/internal/workflow"
	"github.com/spf13/cobra"
)

// clientWorkflowCmd groups workflow operations under the client command.
// They communicate with a running server via its HTTP API and never touch
// the database directly.
var clientWorkflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Manage workflows via the server API",
	Long:  `Import or clone workflow JSON files through a running Nitejaguar server. The server saves them to its database; this command never starts a server or client runner itself.`,
}

var clientWorkflowImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import a workflow JSON file verbatim (upsert)",
	Long:  `Reads a workflow JSON file and POSTs it to the server, which saves it verbatim, keeping ids and name. Overwrites a workflow with the same id. The server must be running.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		return runClientWorkflowFileOp(args[0], false)
	},
}

var clientWorkflowCloneCmd = &cobra.Command{
	Use:   "clone <file>",
	Short: "Clone a workflow JSON file with fresh ids",
	Long:  `Reads a workflow JSON file and POSTs it to the server, which saves an independent copy with fresh workflow_/trigger_/action_ ids, rewritten edges, and a "Clone of: " name prefix. The server must be running.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		return runClientWorkflowFileOp(args[0], true)
	},
}

func init() {
	clientCmd.AddCommand(clientWorkflowCmd)
	clientWorkflowCmd.AddCommand(clientWorkflowImportCmd)
	clientWorkflowCmd.AddCommand(clientWorkflowCloneCmd)
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
