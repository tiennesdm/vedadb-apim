package main

import (
	"bytes"
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

// WebhookEvent represents supported webhook event types.
type WebhookEvent string

const (
	EventAPIPublished     WebhookEvent = "API_PUBLISHED"
	EventAPIUpdated       WebhookEvent = "API_UPDATED"
	EventAPIDeprecated    WebhookEvent = "API_DEPRECATED"
	EventAPISubscribed    WebhookEvent = "API_SUBSCRIBED"
	EventSubscriptionDeleted WebhookEvent = "SUBSCRIPTION_DELETED"
	EventAppCreated       WebhookEvent = "APP_CREATED"
	EventAppUpdated       WebhookEvent = "APP_UPDATED"
	EventAppDeleted       WebhookEvent = "APP_DELETED"
	EventTokenRevoked     WebhookEvent = "TOKEN_REVOKED"
	EventPolicyUpdated    WebhookEvent = "POLICY_UPDATED"
	EventThrottled        WebhookEvent = "THROTTLED"
)

// ValidWebhookEvents returns all valid webhook event types.
func ValidWebhookEvents() []string {
	return []string{
		string(EventAPIPublished),
		string(EventAPIUpdated),
		string(EventAPIDeprecated),
		string(EventAPISubscribed),
		string(EventSubscriptionDeleted),
		string(EventAppCreated),
		string(EventAppUpdated),
		string(EventAppDeleted),
		string(EventTokenRevoked),
		string(EventPolicyUpdated),
		string(EventThrottled),
	}
}

// Webhook represents a registered webhook endpoint.
type Webhook struct {
	ID         string            `json:"id"`
	URL        string            `json:"url"`
	Events     []string          `json:"events"`
	Secret     string            `json:"secret,omitempty"`
	Active     bool              `json:"active"`
	Headers    map[string]string `json:"headers,omitempty"`
	RetryCount int               `json:"retry_count"`
	Timeout    int               `json:"timeout_seconds"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	LastStatus int               `json:"last_status,omitempty"`
	LastError  string            `json:"last_error,omitempty"`
}

// WebhookListResponse is the response for listing webhooks.
type WebhookListResponse struct {
	Count    int       `json:"count"`
	Webhooks []Webhook `json:"webhooks"`
}

// WebhookDelivery represents a webhook delivery attempt.
type WebhookDelivery struct {
	ID        string    `json:"id"`
	WebhookID string    `json:"webhook_id"`
	Event     string    `json:"event"`
	Status    int       `json:"status"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

func newWebhookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Manage webhooks",
		Long: `Register and manage webhook endpoints for receiving event notifications.

Supported events: ` + strings.Join(ValidWebhookEvents(), ", "),
	}

	cmd.AddCommand(newWebhookListCmd())
	cmd.AddCommand(newWebhookCreateCmd())
	cmd.AddCommand(newWebhookDeleteCmd())
	cmd.AddCommand(newWebhookTestCmd())

	return cmd
}

func newWebhookListCmd() *cobra.Command {
	var limit, offset int
	var activeOnly bool

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List registered webhooks",
		Aliases: []string{"ls"},
		Example: "  apim webhook list\n  apim webhook list --active-only",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newAPIClient()

			params := map[string]string{
				"limit":  fmt.Sprintf("%d", limit),
				"offset": fmt.Sprintf("%d", offset),
			}
			if activeOnly {
				params["active"] = "true"
			}

			resp, err := client.Get("/webhooks", params)
			if err != nil {
				return fmt.Errorf("failed to list webhooks: %w", err)
			}
			defer resp.Body.Close()

			var listResp WebhookListResponse
			if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(listResp)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ID\tURL\tEVENTS\tSTATUS\tRETRIES\tLAST_STATUS\tCREATED")
			for _, wh := range listResp.Webhooks {
				status := "active"
				if !wh.Active {
					status = "inactive"
				}
				lastStatus := "-"
				if wh.LastStatus > 0 {
					lastStatus = fmt.Sprintf("%d", wh.LastStatus)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
					truncate(wh.ID, 8),
					truncate(wh.URL, 40),
					truncate(strings.Join(wh.Events, ", "), 30),
					status,
					wh.RetryCount,
					lastStatus,
					wh.CreatedAt.Format("2006-01-02"),
				)
			}
			w.Flush()
			fmt.Printf("\nTotal: %d webhooks\n", listResp.Count)
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "l", 25, "Maximum number of results")
	cmd.Flags().IntVarP(&offset, "offset", "o", 0, "Offset for pagination")
	cmd.Flags().BoolVar(&activeOnly, "active-only", false, "Show only active webhooks")

	return cmd
}

func newWebhookCreateCmd() *cobra.Command {
	var webhookURL string
	var events []string
	var secret string
	var headers map[string]string
	var retryCount, timeout int

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Register a new webhook",
		Example: "  apim webhook create --url https://example.com/webhook --events API_PUBLISHED\n  apim webhook create --url https://example.com/webhook --events API_PUBLISHED,API_UPDATED --secret mysecret",
		RunE: func(cmd *cobra.Command, args []string) error {
			if webhookURL == "" {
				return fmt.Errorf("--url flag is required")
			}
			if len(events) == 0 {
				return fmt.Errorf("--events flag is required (comma-separated list)")
			}

			// Validate events
			validEvents := make(map[string]bool)
			for _, e := range ValidWebhookEvents() {
				validEvents[e] = true
			}
			for _, e := range events {
				if !validEvents[e] {
					return fmt.Errorf("invalid event type: %s. Valid events: %s",
						e, strings.Join(ValidWebhookEvents(), ", "))
				}
			}

			webhook := Webhook{
				URL:        webhookURL,
				Events:     events,
				Secret:     secret,
				Headers:    headers,
				RetryCount: retryCount,
				Timeout:    timeout,
				Active:     true,
			}

			body, err := json.Marshal(webhook)
			if err != nil {
				return fmt.Errorf("failed to marshal request: %w", err)
			}

			client := newAPIClient()
			resp, err := client.Post("/webhooks", "application/json", body)
			if err != nil {
				return fmt.Errorf("failed to create webhook: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("webhook creation failed: %s - %s", resp.Status, string(body))
			}

			var created Webhook
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			fmt.Printf("Webhook created successfully:\n  ID:     %s\n  URL:    %s\n  Events: %s\n  Active: %v\n",
				created.ID, created.URL, strings.Join(created.Events, ", "), created.Active)
			if secret != "" {
				fmt.Println("  Secret: ***")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&webhookURL, "url", "", "Webhook endpoint URL")
	cmd.Flags().StringSliceVar(&events, "events", nil, "Comma-separated list of events to subscribe to")
	cmd.Flags().StringVar(&secret, "secret", "", "Webhook secret for signature verification")
	cmd.Flags().StringToStringVar(&headers, "headers", nil, "Additional HTTP headers (key=value)")
	cmd.Flags().IntVar(&retryCount, "retries", 3, "Number of retry attempts on failure")
	cmd.Flags().IntVar(&timeout, "timeout", 30, "Request timeout in seconds")
	_ = cmd.MarkFlagRequired("url")
	_ = cmd.MarkFlagRequired("events")

	return cmd
}

func newWebhookDeleteCmd() *cobra.Command {
	var id string
	var force bool

	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete a webhook",
		Aliases: []string{"rm", "del"},
		Example: "  apim webhook delete --id <webhook-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			if !force {
				fmt.Printf("Are you sure you want to delete webhook %s? [y/N]: ", id)
				var confirm string
				fmt.Scanln(&confirm)
				if !strings.EqualFold(confirm, "y") {
					fmt.Println("Deletion cancelled.")
					return nil
				}
			}

			client := newAPIClient()
			resp, err := client.Delete("/webhooks/" + id)
			if err != nil {
				return fmt.Errorf("failed to delete webhook: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("webhook deletion failed: %s - %s", resp.Status, string(body))
			}

			fmt.Printf("Webhook %s deleted successfully.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "Webhook ID")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newWebhookTestCmd() *cobra.Command {
	var id, eventType string
	var payload string

	cmd := &cobra.Command{
		Use:     "test",
		Short:   "Send a test event to a webhook",
		Example: "  apim webhook test --id <webhook-id>\n  apim webhook test --id <webhook-id> --event API_PUBLISHED --payload '{\"test\":true}'",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			client := newAPIClient()

			reqBody := map[string]string{
				"event": eventType,
			}
			if payload != "" {
				reqBody["payload"] = payload
			}

			body, err := json.Marshal(reqBody)
			if err != nil {
				return fmt.Errorf("failed to marshal request: %w", err)
			}

			fmt.Printf("Sending test event to webhook %s...\n", id)

			resp, err := client.Post("/webhooks/"+id+"/test", "application/json", body)
			if err != nil {
				return fmt.Errorf("failed to test webhook: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("webhook test failed: %s - %s", resp.Status, string(body))
			}

			var result struct {
				Success   bool   `json:"success"`
				Status    int    `json:"status"`
				Latency   string `json:"latency"`
				Error     string `json:"error,omitempty"`
				Timestamp string `json:"timestamp"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if result.Success {
				fmt.Printf("Webhook test successful!\n")
				fmt.Printf("  Status:    %d\n", result.Status)
				fmt.Printf("  Latency:   %s\n", result.Latency)
				fmt.Printf("  Timestamp: %s\n", result.Timestamp)
			} else {
				fmt.Printf("Webhook test failed!\n")
				fmt.Printf("  Error: %s\n", result.Error)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "Webhook ID")
	cmd.Flags().StringVar(&eventType, "event", "API_PUBLISHED", "Event type for test")
	cmd.Flags().StringVar(&payload, "payload", "", "Custom payload JSON")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}
