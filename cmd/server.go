/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"nitejaguar/cmd/api"

	"github.com/spf13/cobra"
)

var (
	actionName    string
	actionArgs    string
	enableActions bool
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start NiteJaguar in server mode",
	Long:  `Start NiteJaguar in server mode with optional action triggers.`,
	Run: func(cmd *cobra.Command, args []string) {
		api.RunServer()
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
	serverCmd.Flags().StringVarP(&actionName, "action", "a", "filechangeTrigger", "Server action to execute")
	serverCmd.Flags().StringVarP(&actionArgs, "args", "r", "/tmp", "Comma-separated arguments for the server action")
}
