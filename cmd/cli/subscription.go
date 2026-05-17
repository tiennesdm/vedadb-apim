package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// SubscriptionListResponse is the response for listing subscriptions.
type SubscriptionListResponse struct {
	Count         int            `json:"count"`
	Subscriptions []Subscription `json:"subscriptions"`
}

// SubscriptionCreateRequest is used to create a subscription.
type SubscriptionCreateRequest struct {
	APIID  string `json:"api_id"`
	AppID  string `json:"app_id"`
	Tier   string `json:"tier"`
}

func newSubscriptionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "subscribe",
		Short:   "Manage API subscriptions",
		Aliases: []string{"sub", "subscription"},
		Long:    `Subscribe to APIs, list subscriptions, and unsubscribe from APIs.`,
	}

	cmd.AddCommand(newSubscriptionListCmd())
	cmd.AddCommand(newSubscriptionCreateCmd())
	cmd.AddCommand(newSubscriptionDeleteCmd())

	return cmd
}

func newSubscriptionListCmd() *cobra.Command {
	var apiID, appID, status string
	var limit, offset int

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List subscriptions",
		Aliases: []string{"ls"},
		Example: "  apim subscribe list\n  apim subscribe list --api <api-id>\n  apim subscribe list --app <app-id> --status ACTIVE",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newAPIClient()

			params := map[string]string{
				"limit":  fmt.Sprintf("%d", limit),
				"offset": fmt.Sprintf("%d", offset),
			}
			if apiID != "" {
				params["api_id"] = apiID
			}
			if appID != "" {
				params["app_id"] = appID
			}
			if status != "" {
				params["status"] = status
			}

			resp, err := client.Get("/subscriptions", params)
			if err != nil {
				return fmt.Errorf("failed to list subscriptions: %w", err)
			}
			defer resp.Body.Close()

			var listResp SubscriptionListResponse
			if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(listResp)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ID\tAPI_ID\tAPP_ID\tTIER\tSTATUS\tCREATED")
			for _, sub := range listResp.Subscriptions {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					truncate(sub.ID, 8),
					truncate(sub.APIID, 8),
					truncate(sub.AppID, 8),
					sub.Tier,
					sub.Status,
					sub.CreatedAt.Format("2006-01-02"),
				)
			}
			w.Flush()
			fmt.Printf("\nTotal: %d subscriptions\n", listResp.Count)
			return nil
		},
	}

	cmd.Flags().StringVar(&apiID, "api", "", "Filter by API ID")
	cmd.Flags().StringVar(&appID, "app", "", "Filter by application ID")
	cmd.Flags().StringVarP(&status, "status", "s", "", "Filter by status (ACTIVE, BLOCKED, PENDING, REJECTED)")
	cmd.Flags().IntVarP(&limit, "limit", "l", 25, "Maximum number of results")
	cmd.Flags().IntVarP(&offset, "offset", "o", 0, "Offset for pagination")

	return cmd
}

func newSubscriptionCreateCmd() *cobra.Command {
	var apiID, appID, tier string

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Subscribe to an API",
		Aliases: []string{"add", "new"},
		Example: "  apim subscribe create --api <api-id> --app <app-id> --tier Gold\n  apim subscribe --api <api-id> --app <app-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if apiID == "" {
				return fmt.Errorf("--api flag is required")
			}
			if appID == "" {
				return fmt.Errorf("--app flag is required")
			}
			if tier == "" {
				// Default to the application's tier
				client := newAPIClient()
				resp, err := client.Get("/applications/"+appID, nil)
				if err != nil {
					return fmt.Errorf("failed to fetch app for default tier: %w", err)
				}
				defer resp.Body.Close()

				var app Application
				if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
					return fmt.Errorf("failed to decode app: %w", err)
				}
				tier = app.Tier
				if tier == "" {
					tier = "Unlimited"
				}
			}

			req := SubscriptionCreateRequest{
				APIID: apiID,
				AppID: appID,
				Tier:  tier,
			}

			body, err := json.Marshal(req)
			if err != nil {
				return fmt.Errorf("failed to marshal request: %w", err)
			}

			client := newAPIClient()
			resp, err := client.Post("/subscriptions", "application/json", body)
			if err != nil {
				return fmt.Errorf("failed to create subscription: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("subscription creation failed: %s - %s", resp.Status, string(body))
			}

			var created Subscription
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			fmt.Printf("Subscription created successfully:\n  ID:    %s\n  API:   %s\n  App:   %s\n  Tier:  %s\n  Status: %s\n",
				created.ID, created.APIID, created.AppID, created.Tier, created.Status)
			return nil
		},
	}

	cmd.Flags().StringVar(&apiID, "api", "", "API ID to subscribe to")
	cmd.Flags().StringVar(&appID, "app", "", "Application ID")
	cmd.Flags().StringVar(&tier, "tier", "", "Subscription tier (Bronze, Silver, Gold, Platinum, Unlimited)")
	_ = cmd.MarkFlagRequired("api")
	_ = cmd.MarkFlagRequired("app")

	return cmd
}

func newSubscriptionDeleteCmd() *cobra.Command {
	var id string
	var force bool

	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Unsubscribe from an API (delete a subscription)",
		Aliases: []string{"rm", "del", "unsubscribe"},
		Example: "  apim subscribe delete --id <sub-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			if !force {
				fmt.Printf("Are you sure you want to delete subscription %s? [y/N]: ", id)
				var confirm string
				fmt.Scanln(&confirm)
				if !strings.EqualFold(confirm, "y") {
					fmt.Println("Unsubscription cancelled.")
					return nil
				}
			}

			client := newAPIClient()
			resp, err := client.Delete("/subscriptions/" + id)
			if err != nil {
				return fmt.Errorf("failed to delete subscription: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("subscription deletion failed: %s - %s", resp.Status, string(body))
			}

			fmt.Printf("Subscription %s deleted successfully.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "Subscription ID")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}
