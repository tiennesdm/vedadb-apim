// Package main implements the VedaDB API Manager (VAPIM) CLI.
//
// VAPIM is a comprehensive API management platform that provides
// CLI tools for managing APIs, applications, subscriptions, tokens,
// analytics, webhooks, and configuration.
//
// Usage:
//
//	apim login --server https://api.example.com --username admin --password admin123
//	apim api list
//	apim api get --id <api-id>
//	apim api create --file api.yaml
//	apim api publish --id <api-id>
//	apim api delete --id <api-id>
//	apim app list
//	apim app create --name MyApp --tier Silver
//	apim app keys --id <app-id>
//	apim subscribe --api <api-id> --app <app-id> --tier Gold
//	apim token generate --app <app-id>
//	apim analytics --api <api-id> --period 7d
//	apim webhook create --url https://example.com/webhook --events API_PUBLISHED
//	apim config set --key server.url --value https://api.example.com
//	apim config get
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version information set by ldflags during build.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "apim",
		Short: "VedaDB API Manager (VAPIM) CLI",
		Long: `VedaDB API Manager (VAPIM) v2.0 - Enterprise API Management Platform

A comprehensive CLI for managing APIs, applications, subscriptions,
tokens, analytics, webhooks, and platform configuration.

Complete documentation is available at https://docs.vedadata.com/apim`,
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, buildDate),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags
	rootCmd.PersistentFlags().String("server", "", "APIM server URL (overrides config)")
	rootCmd.PersistentFlags().String("token", "", "Authentication token (overrides config)")
	rootCmd.PersistentFlags().Bool("json", false, "Output in JSON format")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")

	// Add all subcommands
	rootCmd.AddCommand(newLoginCmd())
	rootCmd.AddCommand(newLogoutCmd())
	rootCmd.AddCommand(newAPICmd())
	rootCmd.AddCommand(newAppCmd())
	rootCmd.AddCommand(newSubscriptionCmd())
	rootCmd.AddCommand(newTokenCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newAnalyticsCmd())
	rootCmd.AddCommand(newWebhookCmd())

	return rootCmd
}
