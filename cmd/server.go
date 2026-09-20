/*
Copyright © 2025 Sergio Guzman <sergio@nasadmin.com>
*/
package cmd

import (
	"log"
	"os"

	"github.com/mcmx/nitejaguar/cmd/api"
	"github.com/spf13/cobra"
)

var (
	enableActions  bool
	importWorkflow string
	cloneWorkflow  string
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start NiteJaguar in server mode",
	Long:  `Start NiteJaguar in server mode with optional action triggers.`,
	Run: func(cmd *cobra.Command, args []string) {
		if _, err := os.Stat("log/"); err != nil {
			if os.IsNotExist(err) {
				_ = os.Mkdir("log", 0700)
			}
		}
		fileName := "log/server.log"
		// open log file
		logFile, err := os.OpenFile(fileName, os.O_APPEND|os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			log.Panic(err)
		}
		defer func() {
			if err := logFile.Close(); err != nil {
				log.Printf("Error closing log file: %v", err)
			}
		}()

		// set log output
		log.SetOutput(logFile)

		// optional: log date-time, filename, and line number
		// log.SetFlags(log.Lshortfile | log.LstdFlags)
		log.SetFlags(log.LstdFlags)

		sArgs := api.ServerArgs{
			EnableActions:  enableActions,
			ImportWorkflow: importWorkflow,
			CloneWorkflow:  cloneWorkflow,
		}
		api.RunServer(sArgs)
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// serverCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// serverCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	serverCmd.Flags().BoolVarP(&enableActions, "enable-actions", "e", false, "Enable server action")
	serverCmd.Flags().StringVarP(&importWorkflow, "import", "i", "", "Imports a workflow into the database")
	serverCmd.Flags().StringVarP(&cloneWorkflow, "clone", "c", "", "Clones a workflow into the database with new ids")
	// serverCmd.Flags().StringVarP(&actionArgs, "args", "r", "/tmp", "Comma-separated arguments for the server action")
}
