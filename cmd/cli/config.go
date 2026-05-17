package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ConfigFilePath is the default location for CLI configuration.
const ConfigFilePath = "~/.apim/config.yaml"

func initConfig() {
	configPath := os.ExpandEnv(ConfigFilePath)
	configPath = filepath.Clean(strings.Replace(configPath, "~/", getHomeDir()+"/", 1))

	viper.SetConfigType("yaml")
	viper.SetConfigFile(configPath)

	// Create config directory if it doesn't exist
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0750); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create config directory: %v\n", err)
	}

	_ = viper.ReadInConfig()
}

func getHomeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		return home
	}
	return "."
}

func saveConfig() error {
	configPath := os.ExpandEnv(ConfigFilePath)
	configPath = filepath.Clean(strings.Replace(configPath, "~/", getHomeDir()+"/", 1))
	return viper.WriteConfigAs(configPath)
}

func newConfigCmd() *cobra.Command {
	initConfig()

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI configuration",
		Long:  `View and modify CLI configuration stored in ` + ConfigFilePath,
	}

	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigGetCmd())
	cmd.AddCommand(newConfigShowCmd())

	return cmd
}

func newConfigSetCmd() *cobra.Command {
	var key, value string

	cmd := &cobra.Command{
		Use:     "set",
		Short:   "Set a configuration value",
		Example: "  apim config set --key server.url --value https://api.example.com\n  apim config set --key server.username --value admin",
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				return fmt.Errorf("--key flag is required")
			}
			if value == "" {
				return fmt.Errorf("--value flag is required")
			}

			viper.Set(key, value)

			if err := saveConfig(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			// Mask sensitive values in output
			displayVal := value
			if strings.Contains(strings.ToLower(key), "password") ||
				strings.Contains(strings.ToLower(key), "secret") ||
				strings.Contains(strings.ToLower(key), "token") {
				displayVal = "***"
			}

			fmt.Printf("Configuration saved: %s = %s\n", key, displayVal)
			fmt.Printf("Config file: %s\n", viper.ConfigFileUsed())
			return nil
		},
	}

	cmd.Flags().StringVar(&key, "key", "", "Configuration key (dot notation, e.g. server.url)")
	cmd.Flags().StringVar(&value, "value", "", "Configuration value")
	_ = cmd.MarkFlagRequired("key")
	_ = cmd.MarkFlagRequired("value")

	return cmd
}

func newConfigGetCmd() *cobra.Command {
	var key string

	cmd := &cobra.Command{
		Use:     "get",
		Short:   "Get a configuration value",
		Example: "  apim config get --key server.url\n  apim config get",
		RunE: func(cmd *cobra.Command, args []string) error {
			if key != "" {
				value := viper.Get(key)
				if value == nil {
					return fmt.Errorf("key '%s' not found in config", key)
				}

				// Mask sensitive values
				strVal := fmt.Sprintf("%v", value)
				if strings.Contains(strings.ToLower(key), "password") ||
					strings.Contains(strings.ToLower(key), "secret") ||
					strings.Contains(strings.ToLower(key), "token") {
					if strVal != "" {
						strVal = "***"
					}
				}

				fmt.Printf("%s = %s\n", key, strVal)
				return nil
			}

			// List all config values
			allSettings := viper.AllSettings()
			if len(allSettings) == 0 {
				fmt.Println("No configuration found.")
				fmt.Printf("Config file location: %s\n", ConfigFilePath)
				return nil
			}

			if isJSONOutput(cmd) {
				return outputJSON(allSettings)
			}

			fmt.Printf("Configuration (%s):\n\n", viper.ConfigFileUsed())
			printSettings(allSettings, "")
			return nil
		},
	}

	cmd.Flags().StringVar(&key, "key", "", "Configuration key to retrieve")

	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show",
		Short:   "Show all configuration values",
		Aliases: []string{"list", "ls"},
		Example: "  apim config show",
		RunE: func(cmd *cobra.Command, args []string) error {
			allSettings := viper.AllSettings()
			if len(allSettings) == 0 {
				fmt.Println("No configuration found.")
				fmt.Printf("Config file location: %s\n", ConfigFilePath)
				return nil
			}

			if isJSONOutput(cmd) {
				return outputJSON(allSettings)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "KEY\tVALUE")
			printSettingsTabular(w, allSettings, "")
			w.Flush()
			fmt.Printf("\nConfig file: %s\n", viper.ConfigFileUsed())
			return nil
		},
	}
}

func printSettings(settings map[string]interface{}, prefix string) {
	for key, val := range settings {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := val.(type) {
		case map[string]interface{}:
			printSettings(v, fullKey)
		default:
			displayVal := fmt.Sprintf("%v", v)
			if strings.Contains(strings.ToLower(fullKey), "password") ||
				strings.Contains(strings.ToLower(fullKey), "secret") ||
				strings.Contains(strings.ToLower(fullKey), "token") {
				if displayVal != "" && displayVal != "<nil>" {
					displayVal = "***"
				}
			}
			fmt.Printf("  %s = %s\n", fullKey, displayVal)
		}
	}
}

func printSettingsTabular(w *tabwriter.Writer, settings map[string]interface{}, prefix string) {
	for key, val := range settings {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := val.(type) {
		case map[string]interface{}:
			printSettingsTabular(w, v, fullKey)
		default:
			displayVal := fmt.Sprintf("%v", v)
			if strings.Contains(strings.ToLower(fullKey), "password") ||
				strings.Contains(strings.ToLower(fullKey), "secret") ||
				strings.Contains(strings.ToLower(fullKey), "token") {
				if displayVal != "" && displayVal != "<nil>" {
					displayVal = "***"
				}
			}
			fmt.Fprintf(w, "%s\t%s\n", fullKey, displayVal)
		}
	}
}
