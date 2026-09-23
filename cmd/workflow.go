package cmd

import (
	"fmt"
	"os"

	"github.com/mcmx/nitejaguar/internal/database"
	"github.com/mcmx/nitejaguar/internal/workflow"
	"github.com/spf13/cobra"
)

// clientWorkflowCmd groups offline workflow operations under the client
// command. They write directly to the database and do not start the
// server or the client runner.
var clientWorkflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Manage workflows without starting the server",
	Long:  `Import or clone workflow JSON files directly into the database without starting the HTTP server or client runner.`,
}

var clientWorkflowImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import a workflow JSON file verbatim (upsert)",
	Long:  `Reads a workflow JSON file and saves it verbatim, keeping ids and name. Overwrites a workflow with the same id. Does not start the server or client runner.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		return runWorkflowFileOp(args[0], false)
	},
}

var clientWorkflowCloneCmd = &cobra.Command{
	Use:   "clone <file>",
	Short: "Clone a workflow JSON file with fresh ids",
	Long:  `Reads a workflow JSON file and saves an independent copy with fresh workflow_/trigger_/action_ ids, rewritten edges, and a "Clone of: " name prefix. Does not start the server or client runner.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		return runWorkflowFileOp(args[0], true)
	},
}

func init() {
	clientCmd.AddCommand(clientWorkflowCmd)
	clientWorkflowCmd.AddCommand(clientWorkflowImportCmd)
	clientWorkflowCmd.AddCommand(clientWorkflowCloneCmd)
}

func runWorkflowFileOp(path string, clone bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read workflow file %q: %w", path, err)
	}

	db, err := database.New()
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	wm := workflow.NewWorkflowManager(false, db)
	op := "import"
	if clone {
		op = "clone"
		err = wm.CloneWorkflowJSON(string(raw))
	} else {
		err = wm.ImportWorkflowJSON(string(raw))
	}
	if err != nil {
		return fmt.Errorf("%s workflow %q: %w", op, path, err)
	}
	fmt.Printf("%s of %q succeeded\n", op, path)
	return nil
}
