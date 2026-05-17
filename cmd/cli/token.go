package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// TokenResponse represents an OAuth2 token response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
}

// TokenIntrospection represents the response from token introspection.
type TokenIntrospection struct {
	Active    bool   `json:"active"`
	Scope     string `json:"scope,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	Username  string `json:"username,omitempty"`
	TokenType string `json:"token_type,omitempty"`
	Exp       int64  `json:"exp,omitempty"`
	Iat       int64  `json:"iat,omitempty"`
	Nbf       int64  `json:"nbf,omitempty"`
	Sub       string `json:"sub,omitempty"`
	Aud       string `json:"aud,omitempty"`
	Iss       string `json:"iss,omitempty"`
	Jti       string `json:"jti,omitempty"`
}

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Token management commands",
		Long:  `Generate, introspect, and revoke OAuth2 access tokens.`,
	}

	cmd.AddCommand(newTokenGenerateCmd())
	cmd.AddCommand(newTokenIntrospectCmd())
	cmd.AddCommand(newTokenRevokeCmd())

	return cmd
}

func newTokenGenerateCmd() *cobra.Command {
	var appID, keyType string
	var scopes []string
	var validity int64

	cmd := &cobra.Command{
		Use:     "generate",
		Short:   "Generate an access token for an application",
		Aliases: []string{"gen"},
		Example: "  apim token generate --app <app-id>\n  apim token generate --app <app-id> --key-type SANDBOX --scopes read,write\n  apim token generate --app <app-id> --validity 3600",
		RunE: func(cmd *cobra.Command, args []string) error {
			if appID == "" {
				return fmt.Errorf("--app flag is required")
			}

			// Fetch application to get keys
			client := newAPIClient()
			resp, err := client.Get("/applications/"+appID, nil)
			if err != nil {
				return fmt.Errorf("failed to fetch application: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed to fetch application: %s - %s", resp.Status, string(body))
			}

			var app Application
			if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
				return fmt.Errorf("failed to decode application: %w", err)
			}

			// Find matching key
			var key *ApplicationKey
			for _, k := range app.Keys {
				if k.KeyType == keyType {
					key = &k
					break
				}
			}

			if key == nil {
				return fmt.Errorf("no %s keys found for application %s. Generate keys first with: apim app generate-keys --id %s",
					keyType, appID, appID)
			}

			// Build token request
			data := url.Values{}
			data.Set("grant_type", "client_credentials")
			if len(scopes) > 0 {
				data.Set("scope", strings.Join(scopes, " "))
			}
			if validity > 0 {
				data.Set("validity_period", fmt.Sprintf("%d", validity))
			}

			tokenReq, err := http.NewRequest(http.MethodPost, key.TokenEndpoint, strings.NewReader(data.Encode()))
			if err != nil {
				return fmt.Errorf("failed to create token request: %w", err)
			}

			tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			tokenReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
				[]byte(key.ConsumerKey+":"+key.ConsumerSecret)))

			httpClient := &http.Client{Timeout: 30 * time.Second}
			tokenResp, err := httpClient.Do(tokenReq)
			if err != nil {
				return fmt.Errorf("failed to request token: %w", err)
			}
			defer tokenResp.Body.Close()

			if tokenResp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(tokenResp.Body)
				return fmt.Errorf("token generation failed: %s - %s", tokenResp.Status, string(body))
			}

			var tokenRespData TokenResponse
			if err := json.NewDecoder(tokenResp.Body).Decode(&tokenRespData); err != nil {
				return fmt.Errorf("failed to decode token response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(tokenRespData)
			}

			fmt.Println("Token generated successfully:")
			fmt.Printf("  Access Token: %s\n", truncate(tokenRespData.AccessToken, 50))
			fmt.Printf("  Token Type:   %s\n", tokenRespData.TokenType)
			fmt.Printf("  Expires In:   %d seconds\n", tokenRespData.ExpiresIn)
			if tokenRespData.Scope != "" {
				fmt.Printf("  Scope:        %s\n", tokenRespData.Scope)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&appID, "app", "a", "", "Application ID")
	cmd.Flags().StringVar(&keyType, "key-type", "PRODUCTION", "Key type (PRODUCTION or SANDBOX)")
	cmd.Flags().StringSliceVar(&scopes, "scopes", nil, "Requested scopes")
	cmd.Flags().Int64Var(&validity, "validity", 0, "Token validity in seconds (0 = default)")
	_ = cmd.MarkFlagRequired("app")

	return cmd
}

func newTokenIntrospectCmd() *cobra.Command {
	var token string

	cmd := &cobra.Command{
		Use:     "introspect",
		Short:   "Introspect an access token",
		Example: "  apim token introspect --token <access-token>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				return fmt.Errorf("--token flag is required")
			}

			client := newAPIClient()

			// Get server URL for introspection endpoint
			serverURL := viper.GetString("server.url")
			if serverURL == "" {
				serverURL = "http://localhost:9443"
			}

			// Build introspection request
			data := url.Values{}
			data.Set("token", token)

			req, err := http.NewRequest(http.MethodPost,
				strings.TrimSuffix(serverURL, "/")+"/oauth2/introspect",
				strings.NewReader(data.Encode()))
			if err != nil {
				return fmt.Errorf("failed to create introspection request: %w", err)
			}

			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			authToken := viper.GetString("auth.token")
			if authToken != "" {
				req.Header.Set("Authorization", "Bearer "+authToken)
			}

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			if err != nil {
				return fmt.Errorf("failed to introspect token: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("token introspection failed: %s - %s", resp.Status, string(body))
			}

			var result TokenIntrospection
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(result)
			}

			if result.Active {
				fmt.Println("Token Status: ACTIVE")
				fmt.Printf("  Client ID:  %s\n", result.ClientID)
				fmt.Printf("  Username:   %s\n", result.Username)
				fmt.Printf("  Scope:      %s\n", result.Scope)
				fmt.Printf("  Token Type: %s\n", result.TokenType)
				if result.Exp > 0 {
					fmt.Printf("  Expires:    %s\n", time.Unix(result.Exp, 0).Format(time.RFC3339))
				}
				if result.Iss != "" {
					fmt.Printf("  Issuer:     %s\n", result.Iss)
				}
			} else {
				fmt.Println("Token Status: INACTIVE")
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&token, "token", "t", "", "Access token to introspect")
	_ = cmd.MarkFlagRequired("token")

	return cmd
}

func newTokenRevokeCmd() *cobra.Command {
	var token, tokenType string

	cmd := &cobra.Command{
		Use:     "revoke",
		Short:   "Revoke an access token or refresh token",
		Example: "  apim token revoke --token <access-token>\n  apim token revoke --token <refresh-token> --type refresh_token",
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				return fmt.Errorf("--token flag is required")
			}

			serverURL := viper.GetString("server.url")
			if serverURL == "" {
				serverURL = "http://localhost:9443"
			}

			data := url.Values{}
			data.Set("token", token)
			if tokenType != "" {
				data.Set("token_type_hint", tokenType)
			}

			req, err := http.NewRequest(http.MethodPost,
				strings.TrimSuffix(serverURL, "/")+"/oauth2/revoke",
				strings.NewReader(data.Encode()))
			if err != nil {
				return fmt.Errorf("failed to create revoke request: %w", err)
			}

			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			authToken := viper.GetString("auth.token")
			if authToken != "" {
				req.Header.Set("Authorization", "Bearer "+authToken)
			}

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			if err != nil {
				return fmt.Errorf("failed to revoke token: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("token revocation failed: %s - %s", resp.Status, string(body))
			}

			fmt.Printf("Token revoked successfully.\n")
			return nil
		},
	}

	cmd.Flags().StringVarP(&token, "token", "t", "", "Token to revoke")
	cmd.Flags().StringVar(&tokenType, "type", "", "Token type hint (access_token or refresh_token)")
	_ = cmd.MarkFlagRequired("token")

	return cmd
}
