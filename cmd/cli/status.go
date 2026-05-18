// status.go - APIM status/health check command
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// newStatusCmd creates the 'status' command for checking gateway health.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check gateway and services health status",
		Long:  `Check the health status of the APIM gateway and all connected services.`,
		Example: `  apim status
  apim status --server https://api.example.com`,
		RunE: runStatus,
	}
}

func runStatus(cmd *cobra.Command, _ []string) error {
	serverURL, _ := cmd.Flags().GetString("server")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Check gateway health
	gwURL := serverURL + "/health"
	fmt.Println("Checking gateway health...")
	gwStatus := checkHealth(client, gwURL)

	// Check publisher health
	pubURL := serverURL + ":9445/health"
	fmt.Println("Checking publisher health...")
	pubStatus := checkHealth(client, pubURL)

	// Check keymanager health
	kmURL := serverURL + ":9444/health"
	fmt.Println("Checking key manager health...")
	kmStatus := checkHealth(client, kmURL)

	output := map[string]interface{}{
		"gateway":   gwStatus,
		"publisher": pubStatus,
		"keymanager": kmStatus,
		"timestamp": time.Now().UTC(),
	}

	// Check if JSON output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		b, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal output: %w", err)
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Println()
	fmt.Println("=== APIM Status Report ===")
	fmt.Printf("Gateway:    %s\n", statusEmoji(gwStatus))
	fmt.Printf("Publisher:  %s\n", statusEmoji(pubStatus))
	fmt.Printf("KeyManager: %s\n", statusEmoji(kmStatus))
	fmt.Println("==========================")

	return nil
}

func checkHealth(client *http.Client, url string) map[string]interface{} {
	status := map[string]interface{}{
		"url":    url,
		"status": "unknown",
	}

	resp, err := client.Get(url)
	if err != nil {
		status["status"] = "unreachable"
		status["error"] = err.Error()
		return status
	}
	defer resp.Body.Close()

	status["status_code"] = resp.StatusCode
	if resp.StatusCode == http.StatusOK {
		status["status"] = "healthy"
	} else {
		status["status"] = "unhealthy"
	}

	// Try to parse response
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err == nil {
		for k, v := range body {
			if k != "status" {
				status[k] = v
			}
		}
	}

	return status
}

func statusEmoji(status map[string]interface{}) string {
	s, _ := status["status"].(string)
	switch s {
	case "healthy":
		return "UP"
	case "unhealthy":
		return "DEGRADED"
	case "unreachable":
		return "DOWN"
	default:
		return "UNKNOWN"
	}
}
