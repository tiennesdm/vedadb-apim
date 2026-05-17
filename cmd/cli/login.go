package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

// LoginRequest represents the login request payload.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	GrantType string `json:"grant_type"`
	Scope    string `json:"scope,omitempty"`
}

// LoginResponse represents the login response.
type LoginResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

func newLoginCmd() *cobra.Command {
	var serverURL, username, password string

	cmd := &cobra.Command{
		Use:     "login",
		Short:   "Authenticate with the APIM server",
		Long:    `Authenticate with the APIM server and store the access token in the config file.`,
		Example: "  apim login --server https://api.example.com --username admin --password admin123\n  apim login --server https://api.example.com --username admin  # will prompt for password",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for server URL
			if serverURL == "" {
				serverURL = viper.GetString("server.url")
				if serverURL == "" {
					return fmt.Errorf("--server flag is required (or set config server.url)")
				}
			}
			serverURL = strings.TrimSuffix(serverURL, "/")

			// Check for username
			if username == "" {
				username = viper.GetString("server.username")
				if username == "" {
					return fmt.Errorf("--username flag is required (or set config server.username)")
				}
			}

			// Prompt for password if not provided
			if password == "" {
				password = viper.GetString("server.password")
				if password == "" {
					fmt.Fprint(os.Stderr, "Password: ")
					bytePassword, err := term.ReadPassword(int(syscall.Stdin))
					fmt.Fprintln(os.Stderr)
					if err != nil {
						return fmt.Errorf("failed to read password: %w", err)
					}
					password = string(bytePassword)
				}
			}

			if password == "" {
				return fmt.Errorf("password is required")
			}

			// Store server URL
			viper.Set("server.url", serverURL)

			// Perform login
			loginReq := LoginRequest{
				Username:  username,
				Password:  password,
				GrantType: "password",
				Scope:     "apim:api_manage apim:app_manage apim:subscribe",
			}

			reqBody, err := json.Marshal(loginReq)
			if err != nil {
				return fmt.Errorf("failed to marshal login request: %w", err)
			}

			fmt.Printf("Authenticating with %s as %s...\n", serverURL, username)

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Post(
				serverURL+"/api/v2/auth/token",
				"application/json",
				bytes.NewReader(reqBody),
			)
			if err != nil {
				return fmt.Errorf("login request failed: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("login failed: %s - %s", resp.Status, string(body))
			}

			var loginResp LoginResponse
			if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
				return fmt.Errorf("failed to decode login response: %w", err)
			}

			// Store token
			viper.Set("auth.token", loginResp.AccessToken)
			viper.Set("auth.token_type", loginResp.TokenType)
			viper.Set("auth.refresh_token", loginResp.RefreshToken)
			if loginResp.ExpiresIn > 0 {
				loginResp.ExpiresAt = time.Now().Add(time.Duration(loginResp.ExpiresIn) * time.Second)
				viper.Set("auth.expires_at", loginResp.ExpiresAt.Format(time.RFC3339))
			}
			viper.Set("auth.scope", loginResp.Scope)

			if err := saveConfig(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("Login successful!\n")
			fmt.Printf("  Server:      %s\n", serverURL)
			fmt.Printf("  Token type:  %s\n", loginResp.TokenType)
			if loginResp.ExpiresIn > 0 {
				fmt.Printf("  Expires in:  %d seconds\n", loginResp.ExpiresIn)
				fmt.Printf("  Expires at:  %s\n", loginResp.ExpiresAt.Format(time.RFC3339))
			} else {
				fmt.Printf("  Expires in:  unlimited\n")
			}
			if loginResp.Scope != "" {
				fmt.Printf("  Scope:       %s\n", loginResp.Scope)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&serverURL, "server", "", "APIM server URL")
	cmd.Flags().StringVarP(&username, "username", "u", "", "Username for authentication")
	cmd.Flags().StringVarP(&password, "password", "p", "", "Password for authentication")

	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Logout and clear stored authentication token",
		Example: "  apim logout",
		RunE: func(cmd *cobra.Command, args []string) error {
			serverURL := viper.GetString("server.url")
			token := viper.GetString("auth.token")

			// Try to revoke token server-side
			if token != "" && serverURL != "" {
				fmt.Println("Revoking token on server...")
				httpClient := &http.Client{Timeout: 10 * time.Second}

				revokeReq := map[string]string{
					"token": token,
				}
				body, _ := json.Marshal(revokeReq)

				resp, err := httpClient.Post(
					strings.TrimSuffix(serverURL, "/")+"/api/v2/auth/revoke",
					"application/json",
					bytes.NewReader(body),
				)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to revoke token server-side: %v\n", err)
				} else {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						fmt.Println("Token revoked on server.")
					}
				}
			}

			// Clear all auth config
			viper.Set("auth.token", "")
			viper.Set("auth.token_type", "")
			viper.Set("auth.refresh_token", "")
			viper.Set("auth.expires_at", "")
			viper.Set("auth.scope", "")

			if err := saveConfig(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Println("Logged out successfully. Token cleared from local config.")
			return nil
		},
	}
}
