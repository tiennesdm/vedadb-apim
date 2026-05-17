package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// AnalyticsSummary represents the overall analytics summary.
type AnalyticsSummary struct {
	TotalRequests      int64                  `json:"total_requests"`
	TotalErrors        int64                  `json:"total_errors"`
	AverageLatency     float64                `json:"average_latency_ms"`
	RequestsPerSecond  float64                `json:"requests_per_second"`
	TopConsumers       []ConsumerStat         `json:"top_consumers"`
	TopResources       []ResourceStat         `json:"top_resources"`
	StatusDistribution map[string]int64       `json:"status_distribution"`
	TimeSeries         []TimePoint            `json:"time_series"`
	Period             string                 `json:"period"`
	APIID              string                 `json:"api_id,omitempty"`
}

// ConsumerStat represents statistics for a single consumer.
type ConsumerStat struct {
	ConsumerID   string `json:"consumer_id"`
	ConsumerName string `json:"consumer_name"`
	RequestCount int64  `json:"request_count"`
	ErrorCount   int64  `json:"error_count"`
}

// ResourceStat represents statistics for a single resource.
type ResourceStat struct {
	Method       string `json:"method"`
	Path         string `json:"path"`
	RequestCount int64  `json:"request_count"`
	AvgLatency   float64 `json:"avg_latency_ms"`
}

// TimePoint represents a single point in a time series.
type TimePoint struct {
	Timestamp string `json:"timestamp"`
	Count     int64  `json:"count"`
	Errors    int64  `json:"errors"`
	AvgLatency float64 `json:"avg_latency_ms"`
}

// TopAPIsResponse represents the top APIs response.
type TopAPIsResponse struct {
	APIs []APIStat `json:"apis"`
}

// APIStat represents statistics for a single API.
type APIStat struct {
	APIID        string  `json:"api_id"`
	APIName      string  `json:"api_name"`
	Version      string  `json:"version"`
	RequestCount int64   `json:"request_count"`
	ErrorCount   int64   `json:"error_count"`
	AvgLatency   float64 `json:"avg_latency_ms"`
}

func newAnalyticsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analytics",
		Short: "API analytics and usage statistics",
		Long:  `View API analytics, top APIs, and export usage data.`,
	}

	cmd.AddCommand(newAnalyticsSummaryCmd())
	cmd.AddCommand(newAnalyticsTopAPICmd())
	cmd.AddCommand(newAnalyticsExportCmd())

	return cmd
}

func newAnalyticsSummaryCmd() *cobra.Command {
	var apiID, period string

	cmd := &cobra.Command{
		Use:     "summary",
		Short:   "Get analytics summary",
		Example: "  apim analytics summary --api <api-id> --period 7d\n  apim analytics summary --period 24h",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newAPIClient()

			params := map[string]string{
				"period": period,
			}
			if apiID != "" {
				params["api_id"] = apiID
			}

			path := "/analytics/summary"
			if apiID != "" {
				path = "/analytics/summary/" + apiID
			}

			resp, err := client.Get(path, params)
			if err != nil {
				return fmt.Errorf("failed to get analytics: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("analytics request failed: %s - %s", resp.Status, string(body))
			}

			var summary AnalyticsSummary
			if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(summary)
			}

			fmt.Printf("Analytics Summary (period: %s)\n", summary.Period)
			if summary.APIID != "" {
				fmt.Printf("  API: %s\n", summary.APIID)
			}
			fmt.Printf("  Total Requests:   %d\n", summary.TotalRequests)
			fmt.Printf("  Total Errors:     %d\n", summary.TotalErrors)
			fmt.Printf("  Error Rate:       %.2f%%\n", float64(summary.TotalErrors)/float64(max(summary.TotalRequests, 1))*100)
			fmt.Printf("  Avg Latency:      %.2f ms\n", summary.AverageLatency)
			fmt.Printf("  Requests/sec:     %.2f\n", summary.RequestsPerSecond)

			if len(summary.StatusDistribution) > 0 {
				fmt.Println("\nStatus Distribution:")
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "STATUS\tCOUNT")
				for status, count := range summary.StatusDistribution {
					fmt.Fprintf(w, "%s\t%d\n", status, count)
				}
				w.Flush()
			}

			if len(summary.TopConsumers) > 0 {
				fmt.Println("\nTop Consumers:")
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "CONSUMER\tREQUESTS\tERRORS")
				for _, c := range summary.TopConsumers {
					fmt.Fprintf(w, "%s\t%d\t%d\n", c.ConsumerName, c.RequestCount, c.ErrorCount)
				}
				w.Flush()
			}

			if len(summary.TopResources) > 0 {
				fmt.Println("\nTop Resources:")
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "METHOD\tPATH\tREQUESTS\tAVG_LATENCY")
				for _, r := range summary.TopResources {
					fmt.Fprintf(w, "%s\t%s\t%d\t%.2fms\n", r.Method, r.Path, r.RequestCount, r.AvgLatency)
				}
				w.Flush()
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&apiID, "api", "a", "", "Filter by API ID")
	cmd.Flags().StringVarP(&period, "period", "p", "24h", "Time period (1h, 24h, 7d, 30d, 90d)")

	return cmd
}

func newAnalyticsTopAPICmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:     "top-apis",
		Short:   "Show top APIs by usage",
		Example: "  apim analytics top-apis --limit 10",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newAPIClient()

			params := map[string]string{
				"limit": fmt.Sprintf("%d", limit),
			}

			resp, err := client.Get("/analytics/top-apis", params)
			if err != nil {
				return fmt.Errorf("failed to get top APIs: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("request failed: %s - %s", resp.Status, string(body))
			}

			var result TopAPIsResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(result)
			}

			fmt.Println("Top APIs by Usage")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "RANK\tAPI\tVERSION\tREQUESTS\tERRORS\tAVG_LATENCY")
			for i, api := range result.APIs {
				fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%d\t%.2fms\n",
					i+1, api.APIName, api.Version, api.RequestCount, api.ErrorCount, api.AvgLatency)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "Maximum number of results")

	return cmd
}

func newAnalyticsExportCmd() *cobra.Command {
	var apiID, period, format, output string

	cmd := &cobra.Command{
		Use:     "export",
		Short:   "Export analytics data",
		Example: "  apim analytics export --period 7d --format json\n  apim analytics export --api <api-id> --period 30d --format csv --output report.csv",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newAPIClient()

			params := map[string]string{
				"period": period,
				"format": format,
			}
			if apiID != "" {
				params["api_id"] = apiID
			}

			resp, err := client.Get("/analytics/export", params)
			if err != nil {
				return fmt.Errorf("failed to export analytics: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("export failed: %s - %s", resp.Status, string(body))
			}

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("failed to read export data: %w", err)
			}

			// Write to file or stdout
			if output != "" {
				// Ensure directory exists
				dir := filepath.Dir(output)
				if dir != "" && dir != "." {
					if err := os.MkdirAll(dir, 0750); err != nil {
						return fmt.Errorf("failed to create output directory: %w", err)
					}
				}

				if err := os.WriteFile(output, data, 0640); err != nil {
					return fmt.Errorf("failed to write output file: %w", err)
				}
				fmt.Printf("Analytics exported to %s (%d bytes)\n", output, len(data))
			} else {
				fmt.Println(string(data))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&apiID, "api", "a", "", "Filter by API ID")
	cmd.Flags().StringVarP(&period, "period", "p", "24h", "Time period (1h, 24h, 7d, 30d, 90d)")
	cmd.Flags().StringVarP(&format, "format", "f", "json", "Export format (json or csv)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output file path (default: stdout)")

	return cmd
}

// Helper to format analytics as CSV.
func formatAnalyticsCSV(summary *AnalyticsSummary) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	// Write header
	headers := []string{"period", "total_requests", "total_errors", "avg_latency_ms", "requests_per_second"}
	if err := w.Write(headers); err != nil {
		return nil, err
	}

	row := []string{
		summary.Period,
		strconv.FormatInt(summary.TotalRequests, 10),
		strconv.FormatInt(summary.TotalErrors, 10),
		fmt.Sprintf("%.2f", summary.AverageLatency),
		fmt.Sprintf("%.2f", summary.RequestsPerSecond),
	}
	if err := w.Write(row); err != nil {
		return nil, err
	}

	w.Flush()
	return buf.Bytes(), nil
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
