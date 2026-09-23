package cmd

import (
	"os"

	njclient "github.com/mcmx/nitejaguar/internal/client"
	"github.com/spf13/cobra"
)

var (
	clientServer          string
	clientID              string
	clientName            string
	clientToken           string
	clientEnrollmentToken string
)

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Start NiteJaguar in client mode",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return njclient.Run(cmd.Context(), njclient.Config{
			Server: clientServer, ClientID: clientID, Name: clientName, Token: clientToken,
			EnrollmentToken: clientEnrollmentToken,
		}, nil)
	},
}

func init() {
	rootCmd.AddCommand(clientCmd)
	// Persistent so `client workflow import/clone` subcommands inherit them.
	clientCmd.PersistentFlags().StringVar(&clientServer, "server", envOr("NITEJAGUAR_SERVER", "http://127.0.0.1:8080"), "NiteJaguar server URL")
	clientCmd.PersistentFlags().StringVar(&clientID, "client-id", os.Getenv("NITEJAGUAR_CLIENT_ID"), "existing registered client ID")
	clientCmd.PersistentFlags().StringVar(&clientName, "name", envOr("NITEJAGUAR_CLIENT_NAME", "nitejaguar-client"), "client name used during registration")
	clientCmd.PersistentFlags().StringVar(&clientToken, "token", os.Getenv("NITEJAGUAR_TOKEN"), "API token")
	clientCmd.PersistentFlags().StringVar(&clientEnrollmentToken, "enrollment-token", os.Getenv("NITEJAGUAR_ENROLLMENT_TOKEN"), "tenant enrollment/join token for self-registration")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
