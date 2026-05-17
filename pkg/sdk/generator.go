// Package sdk provides SDK code generation capabilities for the VedaDB API Manager.
// It supports generating client SDKs in multiple languages from API definitions.
package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/vedadata/vapim/internal/docs"
)

// Language represents a supported SDK programming language.
type Language string

const (
	LangJavaScript Language = "javascript"
	LangPython     Language = "python"
	LangGo         Language = "go"
	LangJava       Language = "java"
	LangCurl       Language = "curl"
)

// AllLanguages returns all supported languages.
func AllLanguages() []Language {
	return []Language{LangJavaScript, LangPython, LangGo, LangJava, LangCurl}
}

// IsValid checks if a language string is valid.
func (l Language) IsValid() bool {
	switch l {
	case LangJavaScript, LangPython, LangGo, LangJava, LangCurl:
		return true
	}
	return false
}

// String returns the string representation.
func (l Language) String() string {
	return string(l)
}

// FileExtension returns the file extension for the language.
func (l Language) FileExtension() string {
	switch l {
	case LangJavaScript:
		return ".js"
	case LangPython:
		return ".py"
	case LangGo:
		return ".go"
	case LangJava:
		return ".java"
	case LangCurl:
		return ".sh"
	}
	return ".txt"
}

// PackageName returns the package/module name for the language.
func (l Language) PackageName(apiName string) string {
	clean := strings.ToLower(apiName)
	clean = strings.ReplaceAll(clean, " ", "_")
	clean = strings.ReplaceAll(clean, "-", "_")

	switch l {
	case LangJavaScript:
		return clean + "_client"
	case LangPython:
		return clean
	case LangGo:
		return clean
	case LangJava:
		parts := strings.Split(clean, "_")
		for i, p := range parts {
			parts[i] = strings.Title(p)
		}
		return strings.Join(parts, "")
	case LangCurl:
		return ""
	}
	return clean
}

// SDKGenerator defines the interface for SDK generation.
type SDKGenerator interface {
	// Generate creates an SDK for a specific API.
	Generate(apiID string, lang Language) (string, error)
	// GenerateForAll creates SDKs for all available APIs.
	GenerateForAll(lang Language) (map[string]string, error)
	// GenerateFromModel creates an SDK from an API model directly.
	GenerateFromModel(api *docs.APIModel, lang Language) (string, error)
	// SupportedLanguages returns all supported languages.
	SupportedLanguages() []Language
}

// TemplateBasedGenerator generates SDKs using text templates.
type TemplateBasedGenerator struct {
	templates map[Language]*template.Template
	store     APIStore
}

// APIStore provides access to API definitions.
type APIStore interface {
	GetAPI(apiID string) (*docs.APIModel, error)
	ListAPIs() ([]*docs.APIModel, error)
}

// NewTemplateBasedGenerator creates a new template-based SDK generator.
func NewTemplateBasedGenerator(store APIStore) *TemplateBasedGenerator {
	gen := &TemplateBasedGenerator{
		templates: make(map[Language]*template.Template),
		store:     store,
	}
	gen.loadTemplates()
	return gen
}

// NewTemplateBasedGeneratorWithoutStore creates a generator without a store.
// Use GenerateFromModel for direct generation.
func NewTemplateBasedGeneratorWithoutStore() *TemplateBasedGenerator {
	gen := &TemplateBasedGenerator{
		templates: make(map[Language]*template.Template),
	}
	gen.loadTemplates()
	return gen
}

func (g *TemplateBasedGenerator) loadTemplates() {
	for _, lang := range AllLanguages() {
		if tmpl, err := template.New(string(lang)).Funcs(g.templateFuncs()).Parse(getTemplateForLanguage(lang)); err == nil {
			g.templates[lang] = tmpl
		}
	}
}

func (g *TemplateBasedGenerator) templateFuncs() template.FuncMap {
	return template.FuncMap{
		"upper":        strings.ToUpper,
		"lower":        strings.ToLower,
		"title":        strings.Title,
		"camelCase":    toCamelCase,
		"pascalCase":   toPascalCase,
		"snakeCase":    toSnakeCase,
		"kebabCase":    toKebabCase,
		"comment":      formatComment,
		"escapeJS":     escapeJSString,
		"escapePython": escapePythonString,
		"now":          func() string { return time.Now().Format("2006-01-02") },
		"year":         func() int { return time.Now().Year() },
		"httpMethod":   toHTTPMethodConst,
		"sanitizePath": sanitizePath,
		"stripBraces":  stripBraces,
	}
}

// Generate creates an SDK for a specific API.
func (g *TemplateBasedGenerator) Generate(apiID string, lang Language) (string, error) {
	if !lang.IsValid() {
		return "", fmt.Errorf("unsupported language: %s", lang)
	}

	if g.store == nil {
		return "", fmt.Errorf("no API store configured")
	}

	api, err := g.store.GetAPI(apiID)
	if err != nil {
		return "", fmt.Errorf("failed to get API %s: %w", apiID, err)
	}

	return g.GenerateFromModel(api, lang)
}

// GenerateForAll creates SDKs for all available APIs.
func (g *TemplateBasedGenerator) GenerateForAll(lang Language) (map[string]string, error) {
	if !lang.IsValid() {
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}

	if g.store == nil {
		return nil, fmt.Errorf("no API store configured")
	}

	apis, err := g.store.ListAPIs()
	if err != nil {
		return nil, fmt.Errorf("failed to list APIs: %w", err)
	}

	result := make(map[string]string, len(apis))
	for _, api := range apis {
		sdk, err := g.GenerateFromModel(api, lang)
		if err != nil {
			result[api.ID] = fmt.Sprintf("// Error: %v", err)
		} else {
			result[api.ID] = sdk
		}
	}

	return result, nil
}

// GenerateFromModel creates an SDK from an API model directly.
func (g *TemplateBasedGenerator) GenerateFromModel(api *docs.APIModel, lang Language) (string, error) {
	if !lang.IsValid() {
		return "", fmt.Errorf("unsupported language: %s", lang)
	}

	tmpl, ok := g.templates[lang]
	if !ok {
		return "", fmt.Errorf("template not found for language: %s", lang)
	}

	data := g.buildTemplateData(api, lang)

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}

	return buf.String(), nil
}

// SupportedLanguages returns all supported languages.
func (g *TemplateBasedGenerator) SupportedLanguages() []Language {
	return AllLanguages()
}

// TemplateData holds data passed to SDK templates.
type TemplateData struct {
	API          *docs.APIModel
	APIID        string
	APIName      string
	APIVersion   string
	PackageName  string
	EndpointURL  string
	SandboxURL   string
	Context      string
	Resources    []ResourceData
	Headers      []HeaderData
	AuthType     string
	Language     Language
	GeneratedAt  string
	Year         int
}

// ResourceData holds template data for a single resource.
type ResourceData struct {
	Path        string
	Method      string
	MethodUpper string
	Summary     string
	OperationID string
	FunctionName string
	Parameters  []ParamData
	HasBody     bool
	BodyType    string
	ResponseType string
	AuthRequired bool
	Produces    []string
}

// ParamData holds template data for a parameter.
type ParamData struct {
	Name        string
	GoName      string
	Type        string
	In          string // query, path, header, body
	Required    bool
	Description string
	GoType      string
	PythonType  string
	JavaType    string
	JSType      string
}

// HeaderData holds template data for headers.
type HeaderData struct {
	Key   string
	Value string
}

func (g *TemplateBasedGenerator) buildTemplateData(api *docs.APIModel, lang Language) TemplateData {
	data := TemplateData{
		API:         api,
		APIID:       api.ID,
		APIName:     api.Name,
		APIVersion:  api.Version,
		PackageName: lang.PackageName(api.Name),
		EndpointURL: api.EndpointURL,
		SandboxURL:  api.SandboxURL,
		Context:     api.Context,
		AuthType:    "bearer",
		Language:    lang,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Year:        time.Now().Year(),
	}

	// Build resource data
	for _, res := range api.Resources {
		rd := ResourceData{
			Path:         res.Path,
			Method:       res.Method,
			MethodUpper:  strings.ToUpper(res.Method),
			Summary:      res.Summary,
			OperationID:  res.OperationID,
			FunctionName: toCamelCase(res.OperationID),
			HasBody:      res.Method == "POST" || res.Method == "PUT" || res.Method == "PATCH",
			AuthRequired: res.AuthType != "NONE",
			Produces:     res.Produces,
		}

		if rd.FunctionName == "" {
			rd.FunctionName = toCamelCase(fmt.Sprintf("%s_%s", res.Method, sanitizePath(res.Path)))
		}

		for _, param := range res.Parameters {
			pd := ParamData{
				Name:        param.Name,
				GoName:      toPascalCase(param.Name),
				Type:        param.Type,
				In:          param.In,
				Required:    param.Required,
				Description: param.Description,
				GoType:      goTypeFromJSONType(param.Type),
				PythonType:  pythonTypeFromJSONType(param.Type),
				JavaType:    javaTypeFromJSONType(param.Type),
				JSType:      jsTypeFromJSONType(param.Type),
			}
			rd.Parameters = append(rd.Parameters, pd)
		}

		data.Resources = append(data.Resources, rd)
	}

	return data
}

// --- String helpers ---

func toCamelCase(s string) string {
	parts := splitWords(s)
	if len(parts) == 0 {
		return ""
	}
	result := strings.ToLower(parts[0])
	for _, p := range parts[1:] {
		result += strings.Title(strings.ToLower(p))
	}
	return result
}

func toPascalCase(s string) string {
	parts := splitWords(s)
	var result string
	for _, p := range parts {
		result += strings.Title(strings.ToLower(p))
	}
	return result
}

func toSnakeCase(s string) string {
	parts := splitWords(s)
	for i, p := range parts {
		parts[i] = strings.ToLower(p)
	}
	return strings.Join(parts, "_")
}

func toKebabCase(s string) string {
	parts := splitWords(s)
	for i, p := range parts {
		parts[i] = strings.ToLower(p)
	}
	return strings.Join(parts, "-")
}

func splitWords(s string) []string {
	// Replace common separators with spaces
	replacer := strings.NewReplacer(
		"_", " ",
		"-", " ",
		"/", " ",
		"{", "",
		"}", "",
		".", " ",
	)
	s = replacer.Replace(s)
	// Split camelCase
	var result []string
	var current strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' && s[i-1] != ' ' {
			result = append(result, current.String())
			current.Reset()
		}
		if r != ' ' {
			current.WriteRune(r)
		}
		if r == ' ' && current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		result = append(result, current.String())
	}
	return result
}

func formatComment(s string, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + " " + strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

func escapeJSString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

func escapePythonString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func toHTTPMethodConst(method string) string {
	switch strings.ToUpper(method) {
	case "GET":
		return "GET"
	case "POST":
		return "POST"
	case "PUT":
		return "PUT"
	case "PATCH":
		return "PATCH"
	case "DELETE":
		return "DELETE"
	case "HEAD":
		return "HEAD"
	case "OPTIONS":
		return "OPTIONS"
	default:
		return strings.ToUpper(method)
	}
}

func sanitizePath(path string) string {
	path = strings.TrimPrefix(path, "/")
	path = strings.ReplaceAll(path, "/", "_")
	path = strings.ReplaceAll(path, "{", "")
	path = strings.ReplaceAll(path, "}", "")
	path = strings.ReplaceAll(path, "-", "_")
	return path
}

func stripBraces(s string) string {
	s = strings.ReplaceAll(s, "{", "")
	s = strings.ReplaceAll(s, "}", "")
	return s
}

func goTypeFromJSONType(t string) string {
	switch t {
	case "string":
		return "string"
	case "integer":
		return "int64"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		return "[]interface{}"
	case "object":
		return "map[string]interface{}"
	default:
		return "string"
	}
}

func pythonTypeFromJSONType(t string) string {
	switch t {
	case "string":
		return "str"
	case "integer":
		return "int"
	case "number":
		return "float"
	case "boolean":
		return "bool"
	case "array":
		return "list"
	case "object":
		return "dict"
	default:
		return "str"
	}
}

func javaTypeFromJSONType(t string) string {
	switch t {
	case "string":
		return "String"
	case "integer":
		return "Long"
	case "number":
		return "Double"
	case "boolean":
		return "Boolean"
	case "array":
		return "List<Object>"
	case "object":
		return "Map<String, Object>"
	default:
		return "String"
	}
}

func jsTypeFromJSONType(t string) string {
	switch t {
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		return "Array"
	case "object":
		return "Object"
	default:
		return "string"
	}
}

// GenerateSDKPackage creates a complete SDK package as a map of filenames to content.
func (g *TemplateBasedGenerator) GenerateSDKPackage(apiID string, lang Language) (map[string]string, error) {
	if !lang.IsValid() {
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}

	sdk, err := g.Generate(apiID, lang)
	if err != nil {
		return nil, err
	}

	api, err := g.store.GetAPI(apiID)
	if err != nil {
		return nil, err
	}

	pkgName := lang.PackageName(api.Name)
	ext := lang.FileExtension()

	result := map[string]string{
		pkgName + ext: sdk,
	}

	// Add README
	readme, _ := g.generateReadme(api, lang)
	result["README.md"] = readme

	return result, nil
}

func (g *TemplateBasedGenerator) generateReadme(api *docs.APIModel, lang Language) (string, error) {
	tmpl := `# {{.APIName}} SDK

Generated SDK for {{.APIName}} API (v{{.APIVersion}}).

- **Language**: {{.Language}}
- **Generated**: {{.GeneratedAt}}
- **Endpoint**: {{.EndpointURL}}
- **Sandbox**: {{.SandboxURL}}

## Installation

{{if eq .Language "javascript"}}
` + "```bash\nnpm install {{.PackageName}}\n```" + `
{{else if eq .Language "python"}}
` + "```bash\npip install {{.PackageName}}\n```" + `
{{else if eq .Language "go"}}
` + "```bash\ngo get github.com/vedadata/vapim/sdk/{{.PackageName}}\n```" + `
{{else if eq .Language "java"}}
Add to your ` + "`pom.xml`" + `:
` + "```xml\n<dependency>\n  <groupId>com.vedadata</groupId>\n  <artifactId>{{.PackageName}}</artifactId>\n  <version>{{.APIVersion}}</version>\n</dependency>\n```" + `
{{else if eq .Language "curl"}}
Save the curl commands to a shell script:
` + "```bash\nchmod +x {{.PackageName}}.sh\n```" + `
{{end}}

## Quick Start

See the generated client file for usage examples.

## Resources

{{range .Resources}}- **{{.MethodUpper}}** {{.Path}}
{{end}}
`

	data := g.buildTemplateData(api, lang)
	t := template.Must(template.New("readme").Parse(tmpl))

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// getTemplateForLanguage returns the template string for a given language.
func getTemplateForLanguage(lang Language) string {
	switch lang {
	case LangJavaScript:
		return JavaScriptTemplate
	case LangPython:
		return PythonTemplate
	case LangGo:
		return GoTemplate
	case LangJava:
		return JavaTemplate
	case LangCurl:
		return CurlTemplate
	default:
		return ""
	}
}

// JSONMarshal is a utility for marshaling JSON with indentation.
func JSONMarshal(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
