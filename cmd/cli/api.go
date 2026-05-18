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
	"github.com/spf13/viper"
	"github.com/vedadb/vapim/internal/docs"
)

// APIModel represents an API entity in the platform.
type APIModel struct {
	ID              string            `json:"id" yaml:"id"`
	Name            string            `json:"name" yaml:"name"`
	Version         string            `json:"version" yaml:"version"`
	Context         string            `json:"context" yaml:"context"`
	Description     string            `json:"description" yaml:"description"`
	Provider        string            `json:"provider" yaml:"provider"`
	Status          string            `json:"status" yaml:"status"`
	Visibility      string            `json:"visibility" yaml:"visibility"`
	Tier            string            `json:"tier" yaml:"tier"`
	EndpointURL     string            `json:"endpoint_url" yaml:"endpoint_url"`
	SandboxURL      string            `json:"sandbox_url" yaml:"sandbox_url"`
	Transport       []string          `json:"transport" yaml:"transport"`
	Tags            []string          `json:"tags" yaml:"tags"`
	Thumbnail       string            `json:"thumbnail" yaml:"thumbnail"`
	BusinessInfo    *BusinessInfo     `json:"business_info,omitempty" yaml:"business_info,omitempty"`
	Resources       []APIResource     `json:"resources" yaml:"resources"`
	CORSConfig      *CORSConfiguration `json:"cors_config,omitempty" yaml:"cors_config,omitempty"`
	CreatedAt       time.Time         `json:"created_at" yaml:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at" yaml:"updated_at"`
	PublishedAt     *time.Time        `json:"published_at,omitempty" yaml:"published_at,omitempty"`
}

// BusinessInfo contains business metadata for an API.
type BusinessInfo struct {
	Owner       string `json:"owner" yaml:"owner"`
	OwnerEmail  string `json:"owner_email" yaml:"owner_email"`
	Department  string `json:"department" yaml:"department"`
}

// APIResource represents a single resource/endpoint in an API.
type APIResource struct {
	Path        string            `json:"path" yaml:"path"`
	Method      string            `json:"method" yaml:"method"`
	Produces    []string          `json:"produces" yaml:"produces"`
	Consumes    []string          `json:"consumes" yaml:"consumes"`
	AuthType    string            `json:"auth_type" yaml:"auth_type"`
	Throttling  string            `json:"throttling" yaml:"throttling"`
	Parameters  []Parameter       `json:"parameters" yaml:"parameters"`
	Responses   map[string]Response `json:"responses" yaml:"responses"`
}

// Parameter represents a single parameter in a resource.
type Parameter struct {
	Name        string `json:"name" yaml:"name"`
	In          string `json:"in" yaml:"in"` // query, path, header, body
	Description string `json:"description" yaml:"description"`
	Required    bool   `json:"required" yaml:"required"`
	Type        string `json:"type" yaml:"type"`
	Default     string `json:"default,omitempty" yaml:"default,omitempty"`
}

// Response represents a response definition for a resource.
type Response struct {
	Description string                 `json:"description" yaml:"description"`
	Headers     map[string]string      `json:"headers,omitempty" yaml:"headers,omitempty"`
	Schema      map[string]interface{} `json:"schema,omitempty" yaml:"schema,omitempty"`
}

// CORSConfiguration holds CORS settings for an API.
type CORSConfiguration struct {
	Enabled          bool     `json:"enabled" yaml:"enabled"`
	AllowOrigins     []string `json:"allow_origins" yaml:"allow_origins"`
	AllowCredentials bool     `json:"allow_credentials" yaml:"allow_credentials"`
	AllowHeaders     []string `json:"allow_headers" yaml:"allow_headers"`
	AllowMethods     []string `json:"allow_methods" yaml:"allow_methods"`
}

// APIListResponse represents the response from the API list endpoint.
type APIListResponse struct {
	Count int         `json:"count"`
	APIs  []APIModel  `json:"apis"`
}

func newAPICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Manage APIs in the platform",
		Long:  `Create, list, retrieve, update, delete, publish, and deprecate APIs.`,
	}

	cmd.AddCommand(newAPIListCmd())
	cmd.AddCommand(newAPIGetCmd())
	cmd.AddCommand(newAPICreateCmd())
	cmd.AddCommand(newAPIUpdateCmd())
	cmd.AddCommand(newAPIDeleteCmd())
	cmd.AddCommand(newAPIPublishCmd())
	cmd.AddCommand(newAPIDeprecateCmd())
	cmd.AddCommand(newAPISpecCmd())

	return cmd
}

func newAPIListCmd() *cobra.Command {
	var query, provider, status, tier string
	var limit, offset int

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List all APIs",
		Aliases: []string{"ls"},
		Example: "  apim api list\n  apim api list --status PUBLISHED --limit 20\n  apim api list --provider admin --tier Gold",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newAPIClient()

			params := map[string]string{
				"limit":  fmt.Sprintf("%d", limit),
				"offset": fmt.Sprintf("%d", offset),
			}
			if query != "" {
				params["query"] = query
			}
			if provider != "" {
				params["provider"] = provider
			}
			if status != "" {
				params["status"] = status
			}
			if tier != "" {
				params["tier"] = tier
			}

			resp, err := client.Get("/apis", params)
			if err != nil {
				return fmt.Errorf("failed to list APIs: %w", err)
			}
			defer resp.Body.Close()

			var listResp APIListResponse
			if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(listResp)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tVERSION\tCONTEXT\tSTATUS\tTIER\tPROVIDER\tCREATED")
			for _, api := range listResp.APIs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					truncate(api.ID, 8),
					api.Name,
					api.Version,
					api.Context,
					api.Status,
					api.Tier,
					api.Provider,
					api.CreatedAt.Format("2006-01-02"),
				)
			}
			w.Flush()
			fmt.Printf("\nTotal: %d APIs\n", listResp.Count)
			return nil
		},
	}

	cmd.Flags().StringVarP(&query, "query", "q", "", "Search query")
	cmd.Flags().StringVarP(&provider, "provider", "p", "", "Filter by provider")
	cmd.Flags().StringVarP(&status, "status", "s", "", "Filter by status (CREATED, PUBLISHED, DEPRECATED, RETIRED)")
	cmd.Flags().StringVar(&tier, "tier", "", "Filter by tier")
	cmd.Flags().IntVarP(&limit, "limit", "l", 25, "Maximum number of results")
	cmd.Flags().IntVarP(&offset, "offset", "o", 0, "Offset for pagination")

	return cmd
}

func newAPIGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get",
		Short:   "Get API details by ID",
		Example: "  apim api get --id 550e8400-e29b-41d4-a716-446655440000",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _ := cmd.Flags().GetString("id")
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			client := newAPIClient()
			resp, err := client.Get("/apis/"+id, nil)
			if err != nil {
				return fmt.Errorf("failed to get API: %w", err)
			}
			defer resp.Body.Close()

			var api APIModel
			if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			if isJSONOutput(cmd) {
				return outputJSON(api)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "ID:\t\t%s\n", api.ID)
			fmt.Fprintf(w, "Name:\t\t%s\n", api.Name)
			fmt.Fprintf(w, "Version:\t%s\n", api.Version)
			fmt.Fprintf(w, "Context:\t%s\n", api.Context)
			fmt.Fprintf(w, "Description:\t%s\n", api.Description)
			fmt.Fprintf(w, "Provider:\t%s\n", api.Provider)
			fmt.Fprintf(w, "Status:\t\t%s\n", api.Status)
			fmt.Fprintf(w, "Visibility:\t%s\n", api.Visibility)
			fmt.Fprintf(w, "Tier:\t\t%s\n", api.Tier)
			fmt.Fprintf(w, "Endpoint URL:\t%s\n", api.EndpointURL)
			fmt.Fprintf(w, "Sandbox URL:\t%s\n", api.SandboxURL)
			fmt.Fprintf(w, "Transport:\t%s\n", strings.Join(api.Transport, ", "))
			fmt.Fprintf(w, "Tags:\t\t%s\n", strings.Join(api.Tags, ", "))
			fmt.Fprintf(w, "Created:\t%s\n", api.CreatedAt.Format(time.RFC3339))
			fmt.Fprintf(w, "Updated:\t%s\n", api.UpdatedAt.Format(time.RFC3339))
			if api.PublishedAt != nil {
				fmt.Fprintf(w, "Published:\t%s\n", api.PublishedAt.Format(time.RFC3339))
			}
			w.Flush()

			fmt.Println("\nResources:")
			resW := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(resW, "METHOD\tPATH\tAUTH\tTHROTTLING")
			for _, r := range api.Resources {
				fmt.Fprintf(resW, "%s\t%s\t%s\t%s\n", r.Method, r.Path, r.AuthType, r.Throttling)
			}
			resW.Flush()

			return nil
		},
	}
}

func newAPICreateCmd() *cobra.Command {
	var filePath string

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a new API from YAML/JSON file",
		Example: "  apim api create --file api.yaml\n  apim api create --file api.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if filePath == "" {
				return fmt.Errorf("--file flag is required")
			}

			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to read file: %w", err)
			}

			client := newAPIClient()
			resp, err := client.Post("/apis", "application/yaml", data)
			if err != nil {
				return fmt.Errorf("failed to create API: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("API creation failed: %s - %s", resp.Status, string(body))
			}

			var created APIModel
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			fmt.Printf("API created successfully:\n  ID:      %s\n  Name:    %s\n  Context: %s\n  Status:  %s\n",
				created.ID, created.Name, created.Context, created.Status)
			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Path to API definition file (YAML/JSON)")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}

func newAPIUpdateCmd() *cobra.Command {
	var id, filePath string

	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Update an existing API",
		Example: "  apim api update --id <api-id> --file api.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}
			if filePath == "" {
				return fmt.Errorf("--file flag is required")
			}

			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to read file: %w", err)
			}

			client := newAPIClient()
			resp, err := client.Put("/apis/"+id, "application/yaml", data)
			if err != nil {
				return fmt.Errorf("failed to update API: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("API update failed: %s - %s", resp.Status, string(body))
			}

			var updated APIModel
			if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			fmt.Printf("API updated successfully:\n  ID:      %s\n  Name:    %s\n  Updated: %s\n",
				updated.ID, updated.Name, updated.UpdatedAt.Format(time.RFC3339))
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "API ID")
	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Path to updated API definition file")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}

func newAPIDeleteCmd() *cobra.Command {
	var id string
	var force bool

	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete an API by ID",
		Aliases: []string{"rm", "del"},
		Example: "  apim api delete --id <api-id>\n  apim api delete --id <api-id> --force",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			if !force {
				fmt.Printf("Are you sure you want to delete API %s? [y/N]: ", id)
				var confirm string
				fmt.Scanln(&confirm)
				if !strings.EqualFold(confirm, "y") {
					fmt.Println("Deletion cancelled.")
					return nil
				}
			}

			client := newAPIClient()
			resp, err := client.Delete("/apis/" + id)
			if err != nil {
				return fmt.Errorf("failed to delete API: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("API deletion failed: %s - %s", resp.Status, string(body))
			}

			fmt.Printf("API %s deleted successfully.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "API ID to delete")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newAPIPublishCmd() *cobra.Command {
	var id string

	cmd := &cobra.Command{
		Use:     "publish",
		Short:   "Publish an API to make it available for subscription",
		Example: "  apim api publish --id <api-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			client := newAPIClient()
			resp, err := client.Post("/apis/"+id+"/lifecycle", "application/json",
				[]byte(`{"action":"Publish"}`))
			if err != nil {
				return fmt.Errorf("failed to publish API: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("API publish failed: %s - %s", resp.Status, string(body))
			}

			fmt.Printf("API %s published successfully.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "API ID to publish")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newAPIDeprecateCmd() *cobra.Command {
	var id string

	cmd := &cobra.Command{
		Use:     "deprecate",
		Short:   "Deprecate a published API",
		Example: "  apim api deprecate --id <api-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			client := newAPIClient()
			resp, err := client.Post("/apis/"+id+"/lifecycle", "application/json",
				[]byte(`{"action":"Deprecate"}`))
			if err != nil {
				return fmt.Errorf("failed to deprecate API: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("API deprecation failed: %s - %s", resp.Status, string(body))
			}

			fmt.Printf("API %s deprecated successfully.\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&id, "id", "i", "", "API ID to deprecate")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newAPISpecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "spec",
		Short:   "Generate OpenAPI spec for an API",
		Example: "  apim api spec --id <api-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _ := cmd.Flags().GetString("id")
			if id == "" {
				return fmt.Errorf("--id flag is required")
			}

			client := newAPIClient()
			resp, err := client.Get("/apis/"+id, nil)
			if err != nil {
				return fmt.Errorf("failed to get API: %w", err)
			}
			defer resp.Body.Close()

			var api APIModel
			if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}

			spec, err := docs.GenerateOpenAPISpec(&api)
			if err != nil {
				return fmt.Errorf("failed to generate spec: %w", err)
			}

			specJSON, err := json.MarshalIndent(spec, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal spec: %w", err)
			}

			fmt.Println(string(specJSON))
			return nil
		},
	}

	cmd.Flags().StringP("id", "i", "", "API ID")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}

// --- API Client ---

// APIClient handles HTTP communication with the APIM server.
type APIClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func newAPIClient() *APIClient {
	server := viper.GetString("server.url")
	if server == "" {
		server = "http://localhost:9443"
	}

	return &APIClient{
		BaseURL:    strings.TrimSuffix(server, "/"),
		Token:      viper.GetString("auth.token"),
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *APIClient) newRequest(method, path, contentType string, body []byte) (*http.Request, error) {
	url := c.BaseURL + "/api/v2" + path

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	return req, nil
}

func (c *APIClient) Get(path string, queryParams map[string]string) (*http.Response, error) {
	req, err := c.newRequest(http.MethodGet, path, "", nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	for key, val := range queryParams {
		q.Set(key, val)
	}
	req.URL.RawQuery = q.Encode()

	return c.HTTPClient.Do(req)
}

func (c *APIClient) Post(path, contentType string, body []byte) (*http.Response, error) {
	req, err := c.newRequest(http.MethodPost, path, contentType, body)
	if err != nil {
		return nil, err
	}
	return c.HTTPClient.Do(req)
}

func (c *APIClient) Put(path, contentType string, body []byte) (*http.Response, error) {
	req, err := c.newRequest(http.MethodPut, path, contentType, body)
	if err != nil {
		return nil, err
	}
	return c.HTTPClient.Do(req)
}

func (c *APIClient) Delete(path string) (*http.Response, error) {
	req, err := c.newRequest(http.MethodDelete, path, "", nil)
	if err != nil {
		return nil, err
	}
	return c.HTTPClient.Do(req)
}

// --- Helpers ---

func isJSONOutput(cmd *cobra.Command) bool {
	jsonFlag, _ := cmd.Flags().GetBool("json")
	return jsonFlag
}

func outputJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
