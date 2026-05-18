// Package sdkgen generates client SDKs in multiple programming languages
// from an API definition. It provides template-based code generation for
// JavaScript, Python, Go, Java, and cURL.
package sdkgen

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/vedadb/vapim/pkg/models"
)

// Language represents a supported SDK language.
type Language string

const (
	JavaScript Language = "javascript"
	Python     Language = "python"
	GoLang     Language = "go"
	Java       Language = "java"
	CURL       Language = "curl"
)

// AllLanguages returns all supported SDK languages.
func AllLanguages() []Language {
	return []Language{JavaScript, Python, GoLang, Java, CURL}
}

// Generator generates SDK code for a given API definition.
type Generator struct {
	api      *models.API
	endpoint string
}

// NewGenerator creates a new SDK generator for the given API.
func NewGenerator(api *models.API, endpoint string) *Generator {
	return &Generator{
		api:      api,
		endpoint: endpoint,
	}
}

// Generate produces SDK code in the specified language.
func (g *Generator) Generate(lang Language) (string, error) {
	switch lang {
	case JavaScript:
		return g.generateJavaScript()
	case Python:
		return g.generatePython()
	case GoLang:
		return g.generateGo()
	case Java:
		return g.generateJava()
	case CURL:
		return g.generateCURL()
	default:
		return "", fmt.Errorf("unsupported language: %s", lang)
	}
}

// GenerateAll generates SDKs for all supported languages.
func (g *Generator) GenerateAll() map[Language]string {
	result := make(map[Language]string)
	for _, lang := range AllLanguages() {
		code, err := g.Generate(lang)
		if err != nil {
			result[lang] = fmt.Sprintf("// Error: %v", err)
			continue
		}
		result[lang] = code
	}
	return result
}

// SDKPackage represents the data used in SDK templates.
type SDKPackage struct {
	APIName        string
	APIVersion     string
	BaseURL        string
	AuthType       string
	Description    string
	Context        string
	CreatedAt      string
	ClassName      string
	PackageName    string
	Methods        []SDKMethod
	HasOAuth2      bool
	HasAPIKey      bool
	HasBasicAuth   bool
}

// SDKMethod represents a single API endpoint method.
type SDKMethod struct {
	Name        string
	Method      string
	Path        string
	Description string
	AuthRequired bool
	GoMethod    string // HTTP method in Title case for Go
	JSMethod    string // HTTP method in lower case for JS
}

func (g *Generator) buildSDKData(resources []models.APIResource) SDKPackage {
	className := sanitizeClassName(g.api.Name)
	methods := make([]SDKMethod, 0, len(resources))
	
	for _, res := range resources {
		for _, method := range res.Methods {
			methods = append(methods, SDKMethod{
				Name:         sanitizeMethodName(method, res.Path),
				Method:       strings.ToUpper(method),
				Path:         res.Path,
				Description:  res.Description,
				AuthRequired: res.AuthRequired,
				GoMethod:     strings.Title(strings.ToLower(method)),
				JSMethod:     strings.ToLower(method),
			})
		}
	}

	baseURL := g.endpoint
	if baseURL == "" {
		baseURL = g.api.Endpoint
	}

	return SDKPackage{
		APIName:      g.api.Name,
		APIVersion:   g.api.Version,
		BaseURL:      baseURL,
		AuthType:     g.api.AuthType,
		Description:  g.api.Description,
		Context:      g.api.Context,
		CreatedAt:    time.Now().Format("2006-01-02"),
		ClassName:    className,
		PackageName:  strings.ToLower(className),
		Methods:      methods,
		HasOAuth2:    g.api.AuthType == "oauth2",
		HasAPIKey:    g.api.AuthType == "apikey",
		HasBasicAuth: g.api.AuthType == "basic",
	}
}

// --- JavaScript Generator ---

const jsTemplate = `/**
 * {{.APIName}} SDK v{{.APIVersion}}
 * {{.Description}}
 * Generated: {{.CreatedAt}}
 */

class {{.ClassName}}Client {
  constructor(baseURL{{if .HasAPIKey}}, apiKey{{end}}{{if .HasBasicAuth}}, username, password{{end}}) {
    this.baseURL = baseURL || '{{.BaseURL}}';
    {{if .HasAPIKey}}this.apiKey = apiKey;{{end}}
    {{if .HasBasicAuth}}this.username = username; this.password = password;{{end}}
    {{if .HasOAuth2}}this.accessToken = null;{{end}}
  }

  {{if .HasOAuth2}}
  setAccessToken(token) {
    this.accessToken = token;
  }
  {{end}}

  async _request(method, path, body = null) {
    const url = this.baseURL + path;
    const headers = { 'Content-Type': 'application/json' };
    {{if .HasAPIKey}}if (this.apiKey) headers['X-API-Key'] = this.apiKey;{{end}}
    {{if .HasOAuth2}}if (this.accessToken) headers['Authorization'] = 'Bearer ' + this.accessToken;{{end}}
    {{if .HasBasicAuth}}if (this.username) headers['Authorization'] = 'Basic ' + btoa(this.username + ':' + this.password);{{end}}
    
    const opts = { method, headers };
    if (body) opts.body = JSON.stringify(body);
    
    const response = await fetch(url, opts);
    if (!response.ok) throw new Error('HTTP ' + response.status + ': ' + await response.text());
    return response.json();
  }

  {{range .Methods}}
  /**
   * {{.Description}}
   * {{.Method}} {{.Path}}
   */
  async {{.Name}}(body = null) {
    return this._request('{{.JSMethod}}', '{{.Path}}', body);
  }
  {{end}}
}

module.exports = { {{.ClassName}}Client };
`

func (g *Generator) generateJavaScript() (string, error) {
	tmpl, err := template.New("js").Parse(jsTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, g.buildSDKData(nil)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// --- Python Generator ---

const pythonTemplate = `# {{.APIName}} Python SDK v{{.APIVersion}}
# {{.Description}}
# Generated: {{.CreatedAt}}

import requests
from urllib.parse import urljoin

class {{.ClassName}}Client:
    """Client for {{.APIName}} API."""

    def __init__(self, base_url=None{{if .HasAPIKey}}, api_key=None{{end}}{{if .HasBasicAuth}}, username=None, password=None{{end}}):
        self.base_url = (base_url or '{{.BaseURL}}').rstrip('/')
        {{if .HasAPIKey}}self.api_key = api_key{{end}}
        {{if .HasBasicAuth}}self.username = username; self.password = password{{end}}
        {{if .HasOAuth2}}self.access_token = None{{end}}
        self.session = requests.Session()

    {{if .HasOAuth2}}
    def set_access_token(self, token):
        self.access_token = token
    {{end}}

    def _request(self, method, path, json_data=None):
        url = urljoin(self.base_url + '/', path.lstrip('/'))
        headers = {'Content-Type': 'application/json'}
        {{if .HasAPIKey}}if self.api_key: headers['X-API-Key'] = self.api_key{{end}}
        {{if .HasOAuth2}}if self.access_token: headers['Authorization'] = f'Bearer {self.access_token}'{{end}}
        {{if .HasBasicAuth}}if self.username and self.password: headers['Authorization'] = 'Basic ' + base64.b64encode(f'{self.username}:{self.password}'.encode()).decode(){{end}}
        
        response = self.session.request(method, url, headers=headers, json=json_data)
        response.raise_for_status()
        return response.json() if response.content else None

    {{range .Methods}}
    def {{.Name}}(self, data=None):
        """{{.Description}} ({{.Method}} {{.Path}})"""
        return self._request('{{.JSMethod}}', '{{.Path}}', data)
    {{end}}
`

func (g *Generator) generatePython() (string, error) {
	tmpl, err := template.New("python").Parse(pythonTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, g.buildSDKData(nil)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// --- Go Generator ---

const goTemplate = `// {{.APIName}} Go SDK v{{.APIVersion}}
// {{.Description}}
// Generated: {{.CreatedAt}}

package {{.PackageName}}sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client provides access to the {{.APIName}} API.
type Client struct {
	baseURL   string
	httpClient *http.Client
	{{if .HasAPIKey}}apiKey    string{{end}}
	{{if .HasBasicAuth}}username  string
	password  string{{end}}
	{{if .HasOAuth2}}accessToken string{{end}}
}

// NewClient creates a new {{.APIName}} API client.
func NewClient(baseURL string{{if .HasAPIKey}}, apiKey string{{end}}{{if .HasBasicAuth}}, username, password string{{end}}) *Client {
	if baseURL == "" {
		baseURL = "{{.BaseURL}}"
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		{{if .HasAPIKey}}apiKey: apiKey,{{end}}
		{{if .HasBasicAuth}}username: username,
		password: password,{{end}}
	}
}

{{if .HasOAuth2}}
// SetAccessToken configures the OAuth2 bearer token.
func (c *Client) SetAccessToken(token string) {
	c.accessToken = token
}
{{end}}

func (c *Client) doRequest(method, path string, body interface{}) (*http.Response, error) {
	url := c.baseURL + path
	var bodyReader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	{{if .HasAPIKey}}if c.apiKey != "" { req.Header.Set("X-API-Key", c.apiKey) }{{end}}
	{{if .HasOAuth2}}if c.accessToken != "" { req.Header.Set("Authorization", "Bearer "+c.accessToken) }{{end}}
	{{if .HasBasicAuth}}if c.username != "" && c.password != "" { req.Header.Set("Authorization", "Basic "+basicAuth(c.username, c.password)) }{{end}}
	return c.httpClient.Do(req)
}

{{if .HasBasicAuth}}
func basicAuth(u, p string) string {
	return base64.StdEncoding.EncodeToString([]byte(u + ":" + p))
}
{{end}}

{{range .Methods}}
// {{.Name}} calls {{.Method}} {{.Path}}.
func (c *Client) {{.Name}}(body interface{}) (*http.Response, error) {
	return c.doRequest("{{.Method}}", "{{.Path}}", body)
}
{{end}}
`

func (g *Generator) generateGo() (string, error) {
	tmpl, err := template.New("go").Parse(goTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, g.buildSDKData(nil)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// --- Java Generator ---

const javaTemplate = `package com.vedadb.apisdk;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.Base64;

/**
 * {{.APIName}} Java SDK v{{.APIVersion}}
 * {{.Description}}
 * Generated: {{.CreatedAt}}
 */
public class {{.ClassName}}Client {
    private final String baseURL;
    private final HttpClient httpClient;
    {{if .HasAPIKey}}private final String apiKey;{{end}}
    {{if .HasBasicAuth}}private final String username;
    private final String password;{{end}}
    {{if .HasOAuth2}}private String accessToken;{{end}}

    public {{.ClassName}}Client(String baseURL{{if .HasAPIKey}}, String apiKey{{end}}{{if .HasBasicAuth}}, String username, String password{{end}}) {
        this.baseURL = baseURL != null ? baseURL : "{{.BaseURL}}";
        {{if .HasAPIKey}}this.apiKey = apiKey;{{end}}
        {{if .HasBasicAuth}}this.username = username; this.password = password;{{end}}
        this.httpClient = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(30))
            .build();
    }

    {{if .HasOAuth2}}
    public void setAccessToken(String token) {
        this.accessToken = token;
    }
    {{end}}

    private HttpResponse<String> doRequest(String method, String path, String body) throws IOException, InterruptedException {
        String url = this.baseURL + path;
        HttpRequest.Builder builder = HttpRequest.newBuilder()
            .uri(URI.create(url))
            .header("Content-Type", "application/json")
            .timeout(Duration.ofSeconds(30));

        {{if .HasAPIKey}}if (this.apiKey != null) builder.header("X-API-Key", this.apiKey);{{end}}
        {{if .HasOAuth2}}if (this.accessToken != null) builder.header("Authorization", "Bearer " + this.accessToken);{{end}}
        {{if .HasBasicAuth}}if (this.username != null && this.password != null) {
            String auth = Base64.getEncoder().encodeToString((this.username + ":" + this.password).getBytes());
            builder.header("Authorization", "Basic " + auth);
        }{{end}}

        HttpRequest.BodyPublisher bodyPub = body != null ? HttpRequest.BodyPublishers.ofString(body) : HttpRequest.BodyPublishers.noBody();

        switch (method.toUpperCase()) {
            case "GET": builder.GET(); break;
            case "POST": builder.POST(bodyPub); break;
            case "PUT": builder.PUT(bodyPub); break;
            case "PATCH": builder.method("PATCH", bodyPub); break;
            case "DELETE": builder.DELETE(); break;
            default: builder.method(method, bodyPub);
        }

        return httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString());
    }

    {{range .Methods}}
    /**
     * {{.Description}}
     * {{.Method}} {{.Path}}
     */
    public HttpResponse<String> {{.Name}}(String body) throws IOException, InterruptedException {
        return doRequest("{{.Method}}", "{{.Path}}", body);
    }
    {{end}}
}
`

func (g *Generator) generateJava() (string, error) {
	tmpl, err := template.New("java").Parse(javaTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, g.buildSDKData(nil)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// --- cURL Generator ---

const curlTemplate = `#!/bin/bash
# {{.APIName}} cURL Examples
# {{.Description}}
# Generated: {{.CreatedAt}}

BASE_URL="{{.BaseURL}}"
{{if .HasAPIKey}}API_KEY="your-api-key"
{{end}}{{if .HasBasicAuth}}USERNAME="your-username"
PASSWORD="your-password"
{{end}}{{if .HasOAuth2}}ACCESS_TOKEN="your-access-token"
{{end}}
	echo "{{.APIName}} API Examples"
	echo "======================="

{{range .Methods}}
# {{.Description}}
echo "--- {{.Method}} {{.Path}} ---"
curl -X {{.Method}} "${BASE_URL}{{$.Path}}" \\
  -H "Content-Type: application/json" \\
  {{if .AuthRequired}}{{if $.HasAPIKey}}  -H "X-API-Key: ${API_KEY}" \\
  {{end}}{{if $.HasOAuth2}}  -H "Authorization: Bearer ${ACCESS_TOKEN}" \\
  {{end}}{{if $.HasBasicAuth}}  -H "Authorization: Basic $(echo -n ${USERNAME}:${PASSWORD} | base64)" \\
  {{end}}{{end}}  -d '{}'
	echo ""
{{end}}
`

func (g *Generator) generateCURL() (string, error) {
	tmpl, err := template.New("curl").Parse(curlTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, g.buildSDKData(nil)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// --- Utility Functions ---

// sanitizeClassName converts an API name to a valid class name.
func sanitizeClassName(name string) string {
	// Remove special characters and convert to PascalCase
	words := strings.FieldsFunc(name, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.' || r == '/'
	})
	var result strings.Builder
	for _, w := range words {
		if len(w) > 0 {
			result.WriteString(strings.Title(strings.ToLower(w)))
		}
	}
	name = result.String()
	if name == "" {
		name = "API"
	}
	return name
}

// sanitizeMethodName converts an HTTP method and path to a valid method name.
func sanitizeMethodName(method, path string) string {
	// Remove placeholders and special chars from path
	cleanPath := strings.ReplaceAll(path, "/", "_")
	cleanPath = strings.ReplaceAll(cleanPath, "{", "")
	cleanPath = strings.ReplaceAll(cleanPath, "}", "")
	cleanPath = strings.ReplaceAll(cleanPath, "-", "_")
	cleanPath = strings.Trim(cleanPath, "_")

	methodName := strings.ToLower(method)
	if cleanPath != "" {
		return methodName + cleanPath
	}
	return methodName + "request"
}

// GenerateFromAPI creates SDKs for all languages from the given API and resources.
func GenerateFromAPI(api *models.API, endpoint string, resources []models.APIResource) map[Language]string {
	gen := NewGenerator(api, endpoint)
	gen.api = api // Ensure the API is set

	// Build methods from resources
	data := gen.buildSDKData(resources)

	result := make(map[Language]string)
	for _, lang := range AllLanguages() {
		var code string
		var err error
		switch lang {
		case JavaScript:
			tmpl, _ := template.New("js").Parse(jsTemplate)
			var buf bytes.Buffer
			err = tmpl.Execute(&buf, data)
			code = buf.String()
		case Python:
			tmpl, _ := template.New("python").Parse(pythonTemplate)
			var buf bytes.Buffer
			err = tmpl.Execute(&buf, data)
			code = buf.String()
		case GoLang:
			tmpl, _ := template.New("go").Parse(goTemplate)
			var buf bytes.Buffer
			err = tmpl.Execute(&buf, data)
			code = buf.String()
		case Java:
			tmpl, _ := template.New("java").Parse(javaTemplate)
			var buf bytes.Buffer
			err = tmpl.Execute(&buf, data)
			code = buf.String()
		case CURL:
			tmpl, _ := template.New("curl").Parse(curlTemplate)
			var buf bytes.Buffer
			err = tmpl.Execute(&buf, data)
			code = buf.String()
		}
		if err != nil {
			result[lang] = fmt.Sprintf("// Error generating %s SDK: %v", lang, err)
		} else {
			result[lang] = code
		}
	}
	return result
}
