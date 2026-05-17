// Package gateway provides message transformation capabilities for the VedaDB API Manager.
// This file implements JSON-to-XML, XML-to-JSON, header modification, payload transformation,
// and query parameter mapping to support API mediation and protocol translation.
package gateway

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// Transformer defines the interface for request/response transformation.
type Transformer interface {
	// TransformRequest transforms the incoming request before proxying to backend.
	TransformRequest(c *gin.Context) error
	// TransformResponse transforms the backend response before sending to client.
	TransformResponse(c *gin.Context, body []byte, contentType string) ([]byte, string, error)
}

// TransformType defines the type of transformation to apply.
type TransformType string

const (
	// TransformTypeJSONToXML converts JSON payloads to XML.
	TransformTypeJSONToXML TransformType = "JSON_TO_XML"
	// TransformTypeXMLToJSON converts XML payloads to JSON.
	TransformTypeXMLToJSON TransformType = "XML_TO_JSON"
	// TransformTypeHeaderAdd adds headers.
	TransformTypeHeaderAdd TransformType = "HEADER_ADD"
	// TransformTypeHeaderRemove removes headers.
	TransformTypeHeaderRemove TransformType = "HEADER_REMOVE"
	// TransformTypeHeaderReplace replaces header values.
	TransformTypeHeaderReplace TransformType = "HEADER_REPLACE"
	// TransformTypeQueryParamAdd adds query parameters.
	TransformTypeQueryParamAdd TransformType = "QUERY_PARAM_ADD"
	// TransformTypeQueryParamRemove removes query parameters.
	TransformTypeQueryParamRemove TransformType = "QUERY_PARAM_REMOVE"
	// TransformTypePathRewrite rewrites the request path.
	TransformTypePathRewrite TransformType = "PATH_REWRITE"
	// TransformTypeBodyReplace replaces the entire request/response body.
	TransformTypeBodyReplace TransformType = "BODY_REPLACE"
	// TransformTypeJSONata applies a JSONata transformation.
	TransformTypeJSONata TransformType = "JSONATA"
)

// TransformRule defines a single transformation rule.
type TransformRule struct {
	// Type is the transformation type.
	Type TransformType `json:"type" yaml:"type"`
	// Scope defines when to apply: "request" or "response".
	Scope string `json:"scope" yaml:"scope"`
	// Target is the field/header/path to transform.
	Target string `json:"target,omitempty" yaml:"target,omitempty"`
	// Source is the source field/header/path (for mapping operations).
	Source string `json:"source,omitempty" yaml:"source,omitempty"`
	// Value is the new value or expression.
	Value string `json:"value,omitempty" yaml:"value,omitempty"`
	// Condition is an optional condition for applying the rule.
	Condition string `json:"condition,omitempty" yaml:"condition,omitempty"`
	// Priority determines the order of rule application (lower = first).
	Priority int `json:"priority" yaml:"priority"`
	// Enabled controls whether this rule is active.
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// TransformPolicy is a collection of transformation rules.
type TransformPolicy struct {
	ID          string          `json:"id" yaml:"id"`
	Name        string          `json:"name" yaml:"name"`
	Description string          `json:"description,omitempty" yaml:"description,omitempty"`
	APIID       string          `json:"api_id" yaml:"api_id"`
	Rules       []TransformRule `json:"rules" yaml:"rules"`
	Enabled     bool            `json:"enabled" yaml:"enabled"`
}

// DefaultTransformer implements the Transformer interface.
type DefaultTransformer struct {
	rules []TransformRule
}

// NewTransformer creates a new transformer with the given rules.
func NewTransformer(rules []TransformRule) *DefaultTransformer {
	return &DefaultTransformer{
		rules: rules,
	}
}

// TransformRequest applies transformation rules to the incoming request.
func (t *DefaultTransformer) TransformRequest(c *gin.Context) error {
	for _, rule := range t.rules {
		if !rule.Enabled || rule.Scope != "request" {
			continue
		}
		if err := t.applyRule(c, rule, nil, ""); err != nil {
			return fmt.Errorf("transform rule %s failed: %w", rule.Type, err)
		}
	}
	return nil
}

// TransformResponse applies transformation rules to the backend response.
func (t *DefaultTransformer) TransformResponse(c *gin.Context, body []byte, contentType string) ([]byte, string, error) {
	var err error
	for _, rule := range t.rules {
		if !rule.Enabled || rule.Scope != "response" {
			continue
		}
		if err = t.applyRule(c, rule, body, contentType); err != nil {
			return nil, "", fmt.Errorf("transform rule %s failed: %w", rule.Type, err)
		}
	}
	return body, contentType, nil
}

// applyRule applies a single transformation rule.
func (t *DefaultTransformer) applyRule(c *gin.Context, rule TransformRule, body []byte, contentType string) error {
	switch rule.Type {
	case TransformTypeJSONToXML:
		return t.transformJSONToXML(c, rule, body, contentType)
	case TransformTypeXMLToJSON:
		return t.transformXMLToJSON(c, rule, body, contentType)
	case TransformTypeHeaderAdd:
		c.Request.Header.Add(rule.Target, rule.Value)
	case TransformTypeHeaderRemove:
		c.Request.Header.Del(rule.Target)
	case TransformTypeHeaderReplace:
		c.Request.Header.Set(rule.Target, rule.Value)
	case TransformTypeQueryParamAdd:
		q := c.Request.URL.Query()
		q.Add(rule.Target, rule.Value)
		c.Request.URL.RawQuery = q.Encode()
	case TransformTypeQueryParamRemove:
		q := c.Request.URL.Query()
		q.Del(rule.Target)
		c.Request.URL.RawQuery = q.Encode()
	case TransformTypePathRewrite:
		c.Request.URL.Path = t.applyPathRewrite(c.Request.URL.Path, rule.Source, rule.Value)
	case TransformTypeBodyReplace:
		// Body replacement is handled separately for response
	default:
		return fmt.Errorf("unknown transform type: %s", rule.Type)
	}
	return nil
}

// applyPathRewrite rewrites the request path using a regex pattern.
func (t *DefaultTransformer) applyPathRewrite(path, pattern, replacement string) string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return path
	}
	return re.ReplaceAllString(path, replacement)
}

// ---------------------------------------------------------------------------
// JSON to XML Transformation
// ---------------------------------------------------------------------------

// transformJSONToXML converts JSON request body to XML.
func (t *DefaultTransformer) transformJSONToXML(c *gin.Context, rule TransformRule, body []byte, contentType string) error {
	if contentType != "" && contentType != "application/json" {
		return nil // Skip non-JSON
	}

	var data interface{}
	if body != nil {
		if err := json.Unmarshal(body, &data); err != nil {
			return fmt.Errorf("unmarshal JSON: %w", err)
		}
	} else {
		// Read from request body
		if c.Request.Body != nil && c.Request.ContentLength > 0 {
			bodyData, err := io.ReadAll(c.Request.Body)
			if err != nil {
				return fmt.Errorf("read request body: %w", err)
			}
			if err := json.Unmarshal(bodyData, &data); err != nil {
				return fmt.Errorf("unmarshal JSON from request: %w", err)
			}
		}
	}

	xmlBytes, err := convertToXML(data, rule.Target)
	if err != nil {
		return fmt.Errorf("convert to XML: %w", err)
	}

	if body != nil {
		body = xmlBytes
	} else {
		c.Request.Body = io.NopCloser(bytes.NewReader(xmlBytes))
		c.Request.ContentLength = int64(len(xmlBytes))
		c.Request.Header.Set("Content-Type", "application/xml")
	}
	return nil
}

// convertToXML converts a Go data structure to XML bytes.
func convertToXML(data interface{}, rootElement string) ([]byte, error) {
	if rootElement == "" {
		rootElement = "root"
	}
	xmlMap := mapXMLValue(rootElement, data)
	xmlBytes, err := xml.MarshalIndent(xmlMap, "", "  ")
	if err != nil {
		return nil, err
	}
	header := []byte(xml.Header)
	result := append(header, xmlBytes...)
	return result, nil
}

// mapXMLValue recursively maps Go values to XML structure.
func mapXMLValue(key string, value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		elements := make([]xmlMapElement, 0, len(v))
		for k, val := range v {
			elements = append(elements, xmlMapElement{
				XMLName: xml.Name{Local: k},
				Value:   mapXMLValue(k, val),
			})
		}
		return xmlMapEntry{XMLName: xml.Name{Local: key}, Elements: elements}
	case []interface{}:
		elements := make([]xmlMapElement, len(v))
		for i, item := range v {
			elements[i] = xmlMapElement{
				XMLName: xml.Name{Local: "item"},
				Value:   mapXMLValue("item", item),
			}
		}
		return xmlMapEntry{XMLName: xml.Name{Local: key}, Elements: elements}
	case string:
		return xmlMapEntry{XMLName: xml.Name{Local: key}, Value: v}
	case float64:
		return xmlMapEntry{XMLName: xml.Name{Local: key}, Value: fmt.Sprintf("%v", v)}
	case bool:
		return xmlMapEntry{XMLName: xml.Name{Local: key}, Value: fmt.Sprintf("%v", v)}
	case nil:
		return xmlMapEntry{XMLName: xml.Name{Local: key}, Value: ""}
	default:
		return xmlMapEntry{XMLName: xml.Name{Local: key}, Value: fmt.Sprintf("%v", v)}
	}
}

type xmlMapEntry struct {
	XMLName  xml.Name          `xml:"-"`
	Elements []xmlMapElement   `xml:",any"`
	Value    interface{}       `xml:",chardata"`
}

type xmlMapElement struct {
	XMLName xml.Name    `xml:"-"`
	Value   interface{} `xml:",chardata"`
	// For nested elements
	Elements []xmlMapElement `xml:",any,omitempty"`
}

// ---------------------------------------------------------------------------
// XML to JSON Transformation
// ---------------------------------------------------------------------------

// transformXMLToJSON converts XML response body to JSON.
func (t *DefaultTransformer) transformXMLToJSON(c *gin.Context, rule TransformRule, body []byte, contentType string) error {
	if contentType != "" && !strings.Contains(contentType, "xml") {
		return nil // Skip non-XML
	}

	jsonBytes, err := convertXMLToJSON(body)
	if err != nil {
		return fmt.Errorf("convert XML to JSON: %w", err)
	}

	// For response transformation
	if body != nil {
		// This would modify the response body in the middleware chain
		c.Header("Content-Type", "application/json")
	}
	_ = jsonBytes // Will be used by response middleware
	return nil
}

// convertXMLToJSON converts XML bytes to JSON bytes.
func convertXMLToJSON(xmlData []byte) ([]byte, error) {
	var root xmlMapEntry
	if err := xml.Unmarshal(xmlData, &root); err != nil {
		return nil, fmt.Errorf("unmarshal XML: %w", err)
	}

	jsonMap := parseXMLElement(&root)
	return json.Marshal(jsonMap)
}

// parseXMLElement recursively parses XML structure to Go map.
func parseXMLElement(elem *xmlMapEntry) interface{} {
	if elem.Value != nil && len(elem.Elements) == 0 {
		return elem.Value
	}
	if len(elem.Elements) == 1 {
		return parseXMLElement(&xmlMapEntry{Elements: elem.Elements, Value: elem.Value})
	}

	result := make(map[string]interface{})
	for _, child := range elem.Elements {
		key := child.XMLName.Local
		val := parseXMLElement(&xmlMapEntry{Elements: child.Elements, Value: child.Value})

		if existing, ok := result[key]; ok {
			switch v := existing.(type) {
			case []interface{}:
				result[key] = append(v, val)
			default:
				result[key] = []interface{}{v, val}
			}
		} else {
			result[key] = val
		}
	}
	if elem.Value != nil {
		result["#text"] = elem.Value
	}
	return result
}

// ---------------------------------------------------------------------------
// Header Modification
// ---------------------------------------------------------------------------

// HeaderModifier provides utilities for modifying HTTP headers.
type HeaderModifier struct {
	addRules    map[string]string
	removeRules []string
	replaceRules map[string]string
}

// NewHeaderModifier creates a new header modifier.
func NewHeaderModifier() *HeaderModifier {
	return &HeaderModifier{
		addRules:     make(map[string]string),
		removeRules:  make([]string, 0),
		replaceRules: make(map[string]string),
	}
}

// AddRule adds a header addition rule.
func (hm *HeaderModifier) AddRule(header, value string) {
	hm.addRules[header] = value
}

// RemoveRule adds a header removal rule.
func (hm *HeaderModifier) RemoveRule(header string) {
	hm.removeRules = append(hm.removeRules, header)
}

// ReplaceRule adds a header replacement rule.
func (hm *HeaderModifier) ReplaceRule(header, value string) {
	hm.replaceRules[header] = value
}

// Modify applies all header modification rules.
func (hm *HeaderModifier) Modify(headers http.Header) {
	// Remove headers first
	for _, h := range hm.removeRules {
		headers.Del(h)
	}
	// Apply replacements
	for h, v := range hm.replaceRules {
		headers.Set(h, v)
	}
	// Add new headers
	for h, v := range hm.addRules {
		headers.Add(h, v)
	}
}

// ---------------------------------------------------------------------------
// Query Parameter Mapping
// ---------------------------------------------------------------------------

// QueryParamMapper provides query parameter transformation utilities.
type QueryParamMapper struct {
	addRules    map[string]string
	removeRules []string
	renameRules map[string]string // old name -> new name
}

// NewQueryParamMapper creates a new query parameter mapper.
func NewQueryParamMapper() *QueryParamMapper {
	return &QueryParamMapper{
		addRules:    make(map[string]string),
		removeRules: make([]string, 0),
		renameRules: make(map[string]string),
	}
}

// AddRule adds a query parameter.
func (qm *QueryParamMapper) AddRule(param, value string) {
	qm.addRules[param] = value
}

// RemoveRule adds a parameter removal rule.
func (qm *QueryParamMapper) RemoveRule(param string) {
	qm.removeRules = append(qm.removeRules, param)
}

// RenameRule adds a parameter rename rule.
func (qm *QueryParamMapper) RenameRule(oldName, newName string) {
	qm.renameRules[oldName] = newName
}

// Apply applies query parameter transformations to a URL.
func (qm *QueryParamMapper) Apply(u *url.URL) {
	q := u.Query()

	// Remove parameters
	for _, p := range qm.removeRules {
		q.Del(p)
	}

	// Rename parameters
	for oldName, newName := range qm.renameRules {
		if val := q.Get(oldName); val != "" {
			q.Del(oldName)
			q.Set(newName, val)
		}
	}

	// Add new parameters
	for p, v := range qm.addRules {
		q.Set(p, v)
	}

	u.RawQuery = q.Encode()
}

// ---------------------------------------------------------------------------
// Transform Middleware
// ---------------------------------------------------------------------------

// TransformMiddlewareConfig holds configuration for the transform middleware.
type TransformMiddlewareConfig struct {
	Enabled       bool
	RequestRules  []TransformRule
	ResponseRules []TransformRule
	APIID         string
}

// TransformMiddleware is the Gin middleware for request/response transformation.
type TransformMiddleware struct {
	config TransformMiddlewareConfig
}

// NewTransformMiddleware creates a new transform middleware.
func NewTransformMiddleware(config TransformMiddlewareConfig) *TransformMiddleware {
	return &TransformMiddleware{
		config: config,
	}
}

// Middleware returns the Gin handler function for transformation.
func (m *TransformMiddleware) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.config.Enabled {
			c.Next()
			return
		}

		// Apply request transformations
		transformer := NewTransformer(m.config.RequestRules)
		if err := transformer.TransformRequest(c); err != nil {
			// Log error but continue - don't block requests due to transform failure
			c.Header("X-Transform-Error", err.Error())
		}

		c.Next()

		// Apply response transformations
		// Response transformations are handled by the proxy middleware
	}
}

// TransformResponseBody applies transformation rules to a response body.
func TransformResponseBody(body []byte, contentType string, rules []TransformRule) ([]byte, string, error) {
	t := NewTransformer(rules)
	// We create a minimal gin context for transformations
	// In practice, response transformations are applied in the proxy handler
	return t.TransformResponse(nil, body, contentType)
}

// ---------------------------------------------------------------------------
// Utility Functions
// ---------------------------------------------------------------------------

// InjectHeaders adds API context headers to the request.
func InjectHeaders(c *gin.Context, apiID, appID, userID string) {
	c.Request.Header.Set("X-API-ID", apiID)
	c.Request.Header.Set("X-Application-ID", appID)
	c.Request.Header.Set("X-User-Context", userID)
	c.Request.Header.Set("X-Gateway-Name", "VedaDB-APIM")
	c.Request.Header.Set("X-Request-ID", c.GetString("request_id"))
}

// StripGatewayHeaders removes internal gateway headers from the response.
func StripGatewayHeaders(headers http.Header) {
	gatewayHeaders := []string{
		"X-API-ID",
		"X-Application-ID",
		"X-User-Context",
		"X-Gateway-Name",
		"X-Cache",
		"X-Cache-Hits",
		"X-RateLimit-Limit",
		"X-RateLimit-Remaining",
		"X-RateLimit-Reset",
		"X-Transform-Error",
	}
	for _, h := range gatewayHeaders {
		headers.Del(h)
	}
}
