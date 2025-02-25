package main

import (
	"fmt"
	"log"
	"os"

	"nitejaguar/internal/actions"
	"nitejaguar/internal/actions/common"
	"nitejaguar/internal/database"
	"nitejaguar/internal/server"

	"github.com/spf13/cobra"
)

var (
	actionName    string
	actionArgs    string
	enableActions bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "nitejaguar",
		Short: "NiteJaguar - A server/client application",
		Long:  `NiteJaguar is a server/client application that handles triggers and actions.`,
	}

	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Start NiteJaguar in server mode",
		Long:  `Start NiteJaguar in server mode with optional action triggers.`,
		Run: func(cmd *cobra.Command, args []string) {
			runServer()
		},
	}

	clientCmd := &cobra.Command{
		Use:   "client",
		Short: "Start NiteJaguar in client mode",
		Long:  `Start NiteJaguar in client mode to interact with the server.`,
		Run: func(cmd *cobra.Command, args []string) {
			log.Println("Client mode not implemented yet")
		},
	}

	// Add flags to server command
	serverCmd.Flags().BoolVarP(&enableActions, "enable-actions", "e", false, "Enable server action")
	serverCmd.Flags().StringVarP(&actionName, "action", "a", "filechangeTrigger", "Server action to execute")
	serverCmd.Flags().StringVarP(&actionArgs, "args", "r", "/tmp", "Comma-separated arguments for the server action")

	// Add commands to root
	rootCmd.AddCommand(serverCmd, clientCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runServer() {
	myDb := database.New()
	ts := actions.TriggerService{}
	go ts.Run()

	// Handle server action if specified
	if enableActions && actionName != "" {
		args := []string{}
		if actionArgs != "" {
			args = append(args, actionArgs)
		}

		// myArgs := common.ActionArgs{
		// 	ActionName: "filechangeTrigger",
		// 	ActionType: "trigger",
		// 	Name:       "Test filechange trigger",
		// 	Args:       []string{"/tmp"},
		// }

		myArgs := common.ActionArgs{
			ActionName: actionName,
			ActionType: "trigger",
			Name:       fmt.Sprintf("CLI Trigger: %s", actionName),
			Args:       args,
		}

		ts.New(myArgs)
		log.Printf("Created new trigger: %s, with args: %v", actionName, args)
	}

	server := server.NewServer(myDb, ts)
	log.Println("Starting server...")
	err := server.ListenAndServe()
	if err != nil {
		log.Fatalf("Cannot start server: %s", err)
	}
}
