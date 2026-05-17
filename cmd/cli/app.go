package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// Application represents a registered application in the platform.
type Application struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Tier          string            `json:"tier"`
	Status        string            `json:"status"`
	Owner         string            `json:"owner"`
	GroupID       string            `json:"group_id"`
	CallbackURL   string            `json:"callback_url"`
	Keys          []ApplicationKey  `json:"keys,omitempty"`
	Subscriptions []Subscription    `json:"subscriptions,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// ApplicationKey represents an API key pair for an application.
type ApplicationKey struct {
	ID               string `json:"id"`
	KeyType          string `json:"key_type"`          // PRODUCTION, SANDBOX
	ConsumerKey      string `json:"consumer_key"`
	ConsumerSecret   string `json:"consumer_secret"`
	TokenEndpoint    string `json:"token_endpoint"`
	RevokeEndpoint   string `json:"revoke_endpoint"`
	AllowedDomains   []string `json:"allowed_domains"`
	ValidityPeriod   int64    `json:"validity_period"`  // seconds
	GrantTypes       []string `json:"grant_types"`
}

// Subscription represents an API subscription.
type Subscription struct {
	ID        string    `json:"id"`
	APIID     string    `json:"api_id"`
	AppID     string    `json:"app_id"`
	Tier      string    `json:"tier"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ApplicationListResponse is the response for listing applications.
type ApplicationListResponse struct {
	Count        int           `json:"count"`
	Applications []Application `json:"applications"`
}

func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Manage applications",
		Long:  `Create, list, retrieve, and delete applications. Generate and manage API keys.`,
	}

	cmd.AddCommand(newAppListCmd())
	cmd.AddCommand(newAppGetCmd())
	cmd.AddCommand(newAppCreateCmd())
	cmd.AddCommand(newAppDeleteCmd())
	cmd.AddCommand(newAppKeysCmd())
	cmd.AddCommand(newAppGenerateKeysCmd())

	return cmd
}

func newAppListCmd() *cobra.Command {
	var groupID, owner string
	var limit, offset int

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List all applications",
		Aliases: []string{"ls"},
		Example: "  apim app list\n  apim app list --owner admin\n  apim app list --limit 50",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newAPIClient()

			params := map[string]string{
				"limit":  fmt.Sprintf("%d", limit),
				"offset": fmt.Sprintf("%d", offset),
			}
			if groupID != "" {
				params["group_id"] = groupID
			}
			if owner != "" {
				params["owner"] = owner
			}

			resp, err := client.Get("/applications", params)
			if err != nil {
				return fmt.Errorf("failed to list applications: %w", err)
			}
			defer resp.Body.Close()

			var listResp ApplicationListResponse
			if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(listResp)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tTIER\tSTATUS\tOWNER\tKEYS\tSUBS\tCREATED")
			for _, app := range listResp.Applications {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
					truncate(app.ID, 8),
					app.Name,
					app.Tier,
					app.Status,
					app.Owner,
					len(app.Keys),
					len(app.Subscriptions),
					app.CreatedAt.Format("2006-01-02"),
				)
			}
			w.Flush()
			fmt.Printf("\nTotal: %d applications\n", listResp.Count)
			return nil
		},
	}

	cmd.Flags().StringVar(&groupID, "group", "", "Filter by group ID")
	cmd.Flags().StringVar(&owner, "owner", "", "Filter by owner")
	cmd.Flags().IntVarP(&limit, "limit", "l", 25, "Maximum number of results")
	cmd.Flags().IntVarP(&offset, "offset", "o", 0, "Offset for pagination")

	return cmd
}

func newAppGetCmd() *cobra.Command {
	var id string

	cmd := &cobra.Command{
		Use:     "get",
		Short:   "Get application details by ID",
		Example: "  apim app get --id <app-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			client := newAPIClient()
			resp, err := client.Get("/applications/"+id, nil)
			if err != nil {
				return fmt.Errorf("failed to get application: %w", err)
			}
			defer resp.Body.Close()

			var app Application
			if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(app)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "ID:\t\t%s\n", app.ID)
			fmt.Fprintf(w, "Name:\t\t%s\n", app.Name)
			fmt.Fprintf(w, "Description:\t%s\n", app.Description)
			fmt.Fprintf(w, "Tier:\t\t%s\n", app.Tier)
			fmt.Fprintf(w, "Status:\t\t%s\n", app.Status)
			fmt.Fprintf(w, "Owner:\t\t%s\n", app.Owner)
			fmt.Fprintf(w, "Callback URL:\t%s\n", app.CallbackURL)
			fmt.Fprintf(w, "Created:\t%s\n", app.CreatedAt.Format(time.RFC3339))
			fmt.Fprintf(w, "Updated:\t%s\n", app.UpdatedAt.Format(time.RFC3339))
			w.Flush()

			if len(app.Keys) > 0 {
				fmt.Println("\nKeys:")
				kw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				fmt.Fprintln(kw, "TYPE\tCONSUMER_KEY\tVALIDITY\tGRANT_TYPES")
				for _, k := range app.Keys {
					validity := fmt.Sprintf("%ds", k.ValidityPeriod)
					if k.ValidityPeriod == -1 {
						validity = "unlimited"
					}
					fmt.Fprintf(kw, "%s\t%s\t%s\t%s\n",
						k.KeyType,
						truncate(k.ConsumerKey, 20),
						validity,
						join(k.GrantTypes, ","),
					)
				}
				kw.Flush()
			}

			if len(app.Subscriptions) > 0 {
				fmt.Println("\nSubscriptions:")
				sw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				fmt.Fprintln(sw, "ID\tAPI_ID\tTIER\tSTATUS\tCREATED")
				for _, s := range app.Subscriptions {
					fmt.Fprintf(sw, "%s\t%s\t%s\t%s\t%s\n",
						truncate(s.ID, 8),
						truncate(s.APIID, 8),
						s.Tier,
						s.Status,
						s.CreatedAt.Format("2006-01-02"),
					)
				}
				sw.Flush()
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "Application ID")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newAppCreateCmd() *cobra.Command {
	var name, description, tier, callbackURL, groupID, owner string
	var attributes map[string]string

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a new application",
		Example: "  apim app create --name MyApp --tier Silver\n  apim app create --name MyApp --tier Gold --callback https://example.com/callback",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name flag is required")
			}
			if tier == "" {
				return fmt.Errorf("--tier flag is required")
			}

			app := Application{
				Name:        name,
				Description: description,
				Tier:        tier,
				CallbackURL: callbackURL,
				GroupID:     groupID,
				Owner:       owner,
				Attributes:  attributes,
				Status:      "ACTIVE",
			}

			body, err := json.Marshal(app)
			if err != nil {
				return fmt.Errorf("failed to marshal request: %w", err)
			}

			client := newAPIClient()
			resp, err := client.Post("/applications", "application/json", body)
			if err != nil {
				return fmt.Errorf("failed to create application: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("application creation failed: %s - %s", resp.Status, string(body))
			}

			var created Application
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			fmt.Printf("Application created successfully:\n  ID:   %s\n  Name: %s\n  Tier: %s\n",
				created.ID, created.Name, created.Tier)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Application name")
	cmd.Flags().StringVar(&description, "description", "", "Application description")
	cmd.Flags().StringVar(&tier, "tier", "", "Subscription tier (Bronze, Silver, Gold, Platinum, Unlimited)")
	cmd.Flags().StringVar(&callbackURL, "callback", "", "OAuth callback URL")
	cmd.Flags().StringVar(&groupID, "group", "", "Business group ID")
	cmd.Flags().StringVar(&owner, "owner", "", "Application owner")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("tier")

	return cmd
}

func newAppDeleteCmd() *cobra.Command {
	var id string
	var force bool

	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete an application",
		Aliases: []string{"rm", "del"},
		Example: "  apim app delete --id <app-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			if !force {
				fmt.Printf("Are you sure you want to delete application %s? [y/N]: ", id)
				var confirm string
				fmt.Scanln(&confirm)
				if !bytes.EqualFold([]byte(confirm), []byte("y")) && !bytes.EqualFold([]byte(confirm), []byte("Y")) {
					fmt.Println("Deletion cancelled.")
					return nil
				}
			}

			client := newAPIClient()
			resp, err := client.Delete("/applications/" + id)
			if err != nil {
				return fmt.Errorf("failed to delete application: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("application deletion failed: %s - %s", resp.Status, string(body))
			}

			fmt.Printf("Application %s deleted successfully.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "Application ID")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newAppKeysCmd() *cobra.Command {
	var id string

	cmd := &cobra.Command{
		Use:     "keys",
		Short:   "Show application API keys",
		Example: "  apim app keys --id <app-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			client := newAPIClient()
			resp, err := client.Get("/applications/"+id, nil)
			if err != nil {
				return fmt.Errorf("failed to get application: %w", err)
			}
			defer resp.Body.Close()

			var app Application
			if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if len(app.Keys) == 0 {
				fmt.Println("No keys found. Generate keys with: apim app generate-keys --id " + id)
				return nil
			}

			if isJSONOutput(cmd) {
				return outputJSON(app.Keys)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "TYPE\tCONSUMER_KEY\tCONSUMER_SECRET\tVALIDITY\tGRANT_TYPES\tTOKEN_ENDPOINT")
			for _, k := range app.Keys {
				validity := fmt.Sprintf("%ds", k.ValidityPeriod)
				if k.ValidityPeriod == -1 {
					validity = "unlimited"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					k.KeyType,
					truncate(k.ConsumerKey, 20),
					truncate(k.ConsumerSecret, 20),
					validity,
					join(k.GrantTypes, ","),
					k.TokenEndpoint,
				)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "Application ID")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newAppGenerateKeysCmd() *cobra.Command {
	var id, keyType string
	var grantTypes []string
	var validity int64

	cmd := &cobra.Command{
		Use:     "generate-keys",
		Short:   "Generate API keys for an application",
		Example: "  apim app generate-keys --id <app-id>\n  apim app generate-keys --id <app-id> --key-type SANDBOX --validity 3600",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			request := map[string]interface{}{
				"key_type":        keyType,
				"grant_types":     grantTypes,
				"validity_period": validity,
			}

			body, err := json.Marshal(request)
			if err != nil {
				return fmt.Errorf("failed to marshal request: %w", err)
			}

			client := newAPIClient()
			resp, err := client.Post("/applications/"+id+"/keys", "application/json", body)
			if err != nil {
				return fmt.Errorf("failed to generate keys: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("key generation failed: %s - %s", resp.Status, string(body))
			}

			var key ApplicationKey
			if err := json.NewDecoder(resp.Body).Decode(&key); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			fmt.Printf("Keys generated successfully:\n")
			fmt.Printf("  Key Type:      %s\n", key.KeyType)
			fmt.Printf("  Consumer Key:  %s\n", key.ConsumerKey)
			fmt.Printf("  Consumer Secret: %s\n", key.ConsumerSecret)
			fmt.Printf("  Token Endpoint:  %s\n", key.TokenEndpoint)
			fmt.Printf("  Grant Types:   %s\n", join(key.GrantTypes, ", "))
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "Application ID")
	cmd.Flags().StringVar(&keyType, "key-type", "PRODUCTION", "Key type (PRODUCTION or SANDBOX)")
	cmd.Flags().StringSliceVar(&grantTypes, "grant-types", []string{"client_credentials", "password"}, "Allowed grant types")
	cmd.Flags().Int64Var(&validity, "validity", -1, "Token validity in seconds (-1 for unlimited)")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func join(strs []string, sep string) string {
	if len(strs) == 0 {
		return "-"
	}
	var buf bytes.Buffer
	for i, s := range strs {
		if i > 0 {
			buf.WriteString(sep)
		}
		buf.WriteString(s)
	}
	return buf.String()
}
