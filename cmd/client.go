/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// clientCmd represents the client command
var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Start NiteJaguar in client mode",
	Long:  `Start NiteJaguar in client mode to interact with the server.`,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
		fmt.Println("Args:", args)
		fmt.Println("\n\nClient mode not implemented yet, try server instead")
		os.Exit(1)
	},
}

func init() {
	rootCmd.AddCommand(clientCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// clientCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:

	clientCmd.Flags().String("import", "workflow.json", "Imports workflow.json file into the DB")
	clientCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	clientCmd.Flags().BoolVarP(&enableActions, "enable-actions", "e", false, "Enable server action")

}
