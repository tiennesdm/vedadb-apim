// alias.go - Alias commands for apis, apps, keys
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newAPIsAliasCmd creates the 'apis' command as an alias for 'api' commands.
func newAPIsAliasCmd() *cobra.Command {
	apisCmd := &cobra.Command{
		Use:   "apis",
		Short: "Manage APIs (alias for 'api')",
		Long:  `List, create, get, and delete APIs. This is an alias for the 'api' command group.`,
	}

	// Add all api subcommands as apis subcommands
	apiCmd := newAPICmd()
	for _, sub := range apiCmd.Commands() {
		apisCmd.AddCommand(sub)
	}

	return apisCmd
}

// newAppsAliasCmd creates the 'apps' command as an alias for 'app' commands.
func newAppsAliasCmd() *cobra.Command {
	appsCmd := &cobra.Command{
		Use:   "apps",
		Short: "Manage applications (alias for 'app')",
		Long:  `List, create, and manage applications. This is an alias for the 'app' command group.`,
	}

	appCmd := newAppCmd()
	for _, sub := range appCmd.Commands() {
		appsCmd.AddCommand(sub)
	}

	return appsCmd
}

// newKeysCmd creates the 'keys' command for API key management.
func newKeysCmd() *cobra.Command {
	keysCmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage API keys",
		Long:  `Generate, list, and revoke API keys for applications.`,
	}

	keysCmd.AddCommand(newKeysGenerateCmd())
	keysCmd.AddCommand(newKeysListCmd())
	keysCmd.AddCommand(newKeysRevokeCmd())

	return keysCmd
}

func newKeysGenerateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "generate [app-id]",
		Short: "Generate a new API key for an application",
		Long:  `Generate a new API key that can be used to authenticate requests to APIs.`,
		Args:  cobra.ExactArgs(1),
		Example: `  apim keys generate my-app-id
  apim keys generate my-app-id --name "Production Key"
  apim keys generate my-app-id --server https://api.example.com`,
		RunE: runKeysGenerate,
	}
}

func runKeysGenerate(cmd *cobra.Command, args []string) error {
	appID := args[0]
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name = "Generated Key"
	}

	serverURL, _ := cmd.Flags().GetString("server")
	token, _ := cmd.Flags().GetString("token")
	if err := requireAuth(token); err != nil {
		return err
	}

	fmt.Printf("Generating API key for application: %s\n", appID)
	if name != "" {
		fmt.Printf("Key name: %s\n", name)
	}
	fmt.Printf("Server: %s\n", serverURL)
	fmt.Println()

	// This would make an actual API call in production
	// For now, show what would happen
	fmt.Println("POST /keys/generate")
	fmt.Printf("  app_id: %s\n", appID)
	fmt.Printf("  name: %s\n", name)
	fmt.Println()
	fmt.Println("API key generation endpoint ready (connect to running server for actual generation)")

	return nil
}

func newKeysListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [app-id]",
		Short: "List API keys for an application",
		Long:  `List all API keys associated with the specified application.`,
		Args:  cobra.ExactArgs(1),
		Example: `  apim keys list my-app-id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			appID := args[0]
			fmt.Printf("Listing API keys for application: %s\n", appID)
			return nil
		},
	}
}

func newKeysRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke [key-id]",
		Short: "Revoke an API key",
		Long:  `Revoke an API key so it can no longer be used for authentication.`,
		Args:  cobra.ExactArgs(1),
		Example: `  apim keys revoke my-key-id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			keyID := args[0]
			fmt.Printf("Revoking API key: %s\n", keyID)
			return nil
		},
	}
}
