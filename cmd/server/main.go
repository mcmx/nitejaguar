// Command nitejaguar-server is the server-only binary.
//
// It shares all framework and contract code (common, internal/workflow
// types, internal/actions) with the client binary, but never links the
// client runner (internal/client, pion/webrtc). Build with:
//
//	go build -o nitejaguar-server ./cmd/server
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/mcmx/nitejaguar/cmd/api"
	"github.com/mcmx/nitejaguar/internal/version"
	"github.com/spf13/cobra"
)

var (
	enableActions  bool
	importWorkflow string
	cloneWorkflow  string
)

func runServer(_ *cobra.Command, _ []string) {
	if _, err := os.Stat("log/"); err != nil {
		if os.IsNotExist(err) {
			_ = os.Mkdir("log", 0o700)
		}
	}
	logFile, err := os.OpenFile("log/server.log", os.O_APPEND|os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		log.Panic(err)
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			log.Printf("Error closing log file: %v", err)
		}
	}()
	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags)

	api.RunServer(api.ServerArgs{
		EnableActions:  enableActions,
		ImportWorkflow: importWorkflow,
		CloneWorkflow:  cloneWorkflow,
	})
}

func main() {
	rootCmd := &cobra.Command{
		Use:     "nitejaguar-server",
		Short:   "Start NiteJaguar in server mode",
		Long:    `Start NiteJaguar in server mode with optional action triggers.`,
		Version: version.Version,
		Run:     runServer,
	}
	// `nitejaguar-server server -e` stays working as an alias for the root
	// command so existing scripts and `make run` keep functioning.
	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Start NiteJaguar in server mode",
		Run:   runServer,
	}
	rootCmd.AddCommand(serverCmd)

	rootCmd.PersistentFlags().BoolVarP(&enableActions, "enable-actions", "e", false, "Enable server action")
	rootCmd.PersistentFlags().StringVarP(&importWorkflow, "import", "i", "", "Imports a workflow into the database")
	rootCmd.PersistentFlags().StringVarP(&cloneWorkflow, "clone", "c", "", "Clones a workflow into the database with new ids")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
