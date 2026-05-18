// Package gateway provides request/response validation middleware for the
// VedaDB API Manager Gateway.
package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tiennesdm/vedadb-apim/pkg/store"
)

// ---------------------------------------------------------------------------
// ValidationMiddleware
// ---------------------------------------------------------------------------

// ValidationMiddleware validates incoming requests against API resource schemas
// stored in the database. It looks up request schemas via the store's schema
// API and validates the request body against them before the handler runs.
func ValidationMiddleware(store store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiID := c.GetString("apiID")
		if apiID == "" {
			c.Next()
			return
		}

		// Only validate requests with a body.
		if c.Request.Body == nil || c.Request.ContentLength == 0 {
			c.Next()
			return
		}

		// Fetch resources for this API from the database.
		resources, err := store.GetResourcesByAPI(apiID)
		if err != nil {
			c.Next()
			return
		}

		method := c.Request.Method
		path := c.Request.URL.Path

		for _, r := range resources {
			if r.Method == method && pathMatchesResource(r.Path, path) {
				// Look up a request schema for this resource.
				schemaRec, err := store.GetSchema(r.ID, "request")
				if err != nil || schemaRec == nil || schemaRec.SchemaJSON == "" {
					// No schema defined for this resource; skip validation.
					break
				}

				schema := schemaRec.SchemaJSON
				if !looksLikeJSONSchema(schema) {
					break
				}

				body, err := io.ReadAll(c.Request.Body)
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{
						"error":   "validation_failed",
						"message": "Unable to read request body",
					})
					c.Abort()
					return
				}
				// Restore body so downstream handlers can read it.
				c.Request.Body = io.NopCloser(bytes.NewReader(body))

				if valErr := validateJSONSchema(body, schema); valErr != nil {
					c.JSON(http.StatusBadRequest, gin.H{
						"error":   "validation_failed",
						"details": valErr.Error(),
					})
					c.Abort()
					return
				}
				break
			}
		}

		c.Next()
	}
}

// ---------------------------------------------------------------------------
// JSON Schema validation (real implementation)
// ---------------------------------------------------------------------------

// jsonSchema is a minimal but real JSON Schema representation that supports
// type checking, required fields, string/numeric constraints, and nested
// objects.
type jsonSchema struct {
	Type       string                 `json:"type"`
	Required   []string               `json:"required"`
	Properties map[string]*jsonSchema `json:"properties"`
	MinLength  *int                   `json:"minLength"`
	MaxLength  *int                   `json:"maxLength"`
	Pattern    string                 `json:"pattern"`
	Minimum    *float64               `json:"minimum"`
	Maximum    *float64               `json:"maximum"`
	MinItems   *int                   `json:"minItems"`
	MaxItems   *int                   `json:"maxItems"`
	Items      *jsonSchema            `json:"items"`
	Enum       []interface{}          `json:"enum"`
	Description string                `json:"description"`
}

// looksLikeJSONSchema heuristically checks if a string is a JSON schema.
func looksLikeJSONSchema(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") && strings.Contains(s, "\"type\"")
}

// pathMatchesResource checks if a request path matches a resource path pattern.
// It supports exact matches and path parameters like /users/{id}.
func pathMatchesResource(resourcePath, requestPath string) bool {
	if resourcePath == requestPath {
		return true
	}

	rp := strings.TrimSuffix(resourcePath, "/")
	rq := strings.TrimSuffix(requestPath, "/")
	if rp == rq {
		return true
	}

	resourceParts := strings.Split(rp, "/")
	requestParts := strings.Split(rq, "/")
	if len(resourceParts) != len(requestParts) {
		return false
	}
	for i := range resourceParts {
		if strings.HasPrefix(resourceParts[i], "{") && strings.HasSuffix(resourceParts[i], "}") {
			continue
		}
		if resourceParts[i] != requestParts[i] {
			return false
		}
	}
	return true
}

// validateJSONSchema validates data (as JSON bytes) against a JSON schema string.
func validateJSONSchema(data []byte, schemaStr string) error {
	var schema jsonSchema
	if err := json.Unmarshal([]byte(schemaStr), &schema); err != nil {
		return fmt.Errorf("invalid JSON schema: %w", err)
	}

	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}

	return validateValue(value, &schema, "$")
}

// validateValue recursively validates a parsed JSON value against a schema.
func validateValue(value interface{}, schema *jsonSchema, path string) error {
	if schema == nil {
		return nil
	}

	// Check enum constraint.
	if len(schema.Enum) > 0 {
		found := false
		for _, ev := range schema.Enum {
			if jsonEqual(value, ev) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: value must be one of %v, got %v", path, schema.Enum, value)
		}
	}

	switch schema.Type {
	case "object":
		obj, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s: expected object, got %T", path, value)
		}
		for _, req := range schema.Required {
			if _, exists := obj[req]; !exists {
				return fmt.Errorf("%s: missing required field %q", path, req)
			}
		}
		for key, propSchema := range schema.Properties {
			if propVal, exists := obj[key]; exists {
				childPath := path + "." + key
				if err := validateValue(propVal, propSchema, childPath); err != nil {
					return err
				}
			}
		}

	case "array":
		arr, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("%s: expected array, got %T", path, value)
		}
		if schema.MinItems != nil && len(arr) < *schema.MinItems {
			return fmt.Errorf("%s: array must have at least %d items, got %d", path, *schema.MinItems, len(arr))
		}
		if schema.MaxItems != nil && len(arr) > *schema.MaxItems {
			return fmt.Errorf("%s: array must have at most %d items, got %d", path, *schema.MaxItems, len(arr))
		}
		if schema.Items != nil {
			for i, item := range arr {
				childPath := fmt.Sprintf("%s[%d]", path, i)
				if err := validateValue(item, schema.Items, childPath); err != nil {
					return err
				}
			}
		}

	case "string":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: expected string, got %T", path, value)
		}
		if schema.MinLength != nil && len(s) < *schema.MinLength {
			return fmt.Errorf("%s: string length %d is less than minimum %d", path, len(s), *schema.MinLength)
		}
		if schema.MaxLength != nil && len(s) > *schema.MaxLength {
			return fmt.Errorf("%s: string length %d exceeds maximum %d", path, len(s), *schema.MaxLength)
		}
		if schema.Pattern != "" {
			re, err := regexp.Compile(schema.Pattern)
			if err != nil {
				return fmt.Errorf("%s: invalid pattern %q: %w", path, schema.Pattern, err)
			}
			if !re.MatchString(s) {
				return fmt.Errorf("%s: value %q does not match pattern %q", path, s, schema.Pattern)
			}
		}

	case "integer":
		var num float64
		switch v := value.(type) {
		case float64:
			num = v
		case int:
			num = float64(v)
		case int64:
			num = float64(v)
		case json.Number:
			n, err := v.Float64()
			if err != nil {
				return fmt.Errorf("%s: expected integer, got %v", path, value)
			}
			num = n
		default:
			return fmt.Errorf("%s: expected integer, got %T", path, value)
		}
		if num != float64(int64(num)) {
			return fmt.Errorf("%s: expected integer, got %f", path, num)
		}
		if schema.Minimum != nil && num < *schema.Minimum {
			return fmt.Errorf("%s: value %v is less than minimum %v", path, num, *schema.Minimum)
		}
		if schema.Maximum != nil && num > *schema.Maximum {
			return fmt.Errorf("%s: value %v exceeds maximum %v", path, num, *schema.Maximum)
		}

	case "number":
		var num float64
		switch v := value.(type) {
		case float64:
			num = v
		case int:
			num = float64(v)
		case int64:
			num = float64(v)
		case json.Number:
			n, err := v.Float64()
			if err != nil {
				return fmt.Errorf("%s: expected number, got %v", path, value)
			}
			num = n
		default:
			return fmt.Errorf("%s: expected number, got %T", path, value)
		}
		if schema.Minimum != nil && num < *schema.Minimum {
			return fmt.Errorf("%s: value %v is less than minimum %v", path, num, *schema.Minimum)
		}
		if schema.Maximum != nil && num > *schema.Maximum {
			return fmt.Errorf("%s: value %v exceeds maximum %v", path, num, *schema.Maximum)
		}

	case "boolean":
		_, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%s: expected boolean, got %T", path, value)
		}

	case "null":
		if value != nil {
			return fmt.Errorf("%s: expected null, got %v", path, value)
		}
	}

	return nil
}

// jsonEqual compares two JSON-decoded values for equality.
func jsonEqual(a, b interface{}) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case nil:
		return b == nil
	default:
		return false
	}
}

// ValidateJSONSchemaString is a helper that validates a JSON string directly
// against a schema string and returns human-readable errors.
func ValidateJSONSchemaString(data, schema string) error {
	return validateJSONSchema([]byte(data), schema)
}

// CompileJSONSchema parses a JSON schema string and returns any parse error.
func CompileJSONSchema(schemaStr string) error {
	var schema jsonSchema
	if err := json.Unmarshal([]byte(schemaStr), &schema); err != nil {
		return fmt.Errorf("invalid JSON schema: %w", err)
	}
	if schema.Type == "" && len(schema.Properties) == 0 && len(schema.Required) == 0 {
		return fmt.Errorf("empty schema: no type, properties, or required fields defined")
	}
	return nil
}

// ParseSchemaConstraints extracts human-readable constraint descriptions from a
// JSON schema string for documentation purposes.
func ParseSchemaConstraints(schemaStr string) (map[string]string, error) {
	var schema jsonSchema
	if err := json.Unmarshal([]byte(schemaStr), &schema); err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for name, prop := range schema.Properties {
		desc := prop.Type
		if prop.Description != "" {
			desc = prop.Description
		}
		if containsStr(schema.Required, name) {
			desc += " (required)"
		}
		if prop.Minimum != nil {
			desc += fmt.Sprintf(" min:%s", strconv.FormatFloat(*prop.Minimum, 'f', -1, 64))
		}
		if prop.Maximum != nil {
			desc += fmt.Sprintf(" max:%s", strconv.FormatFloat(*prop.Maximum, 'f', -1, 64))
		}
		if prop.MinLength != nil {
			desc += fmt.Sprintf(" minLen:%d", *prop.MinLength)
		}
		if prop.MaxLength != nil {
			desc += fmt.Sprintf(" maxLen:%d", *prop.MaxLength)
		}
		if prop.Pattern != "" {
			desc += fmt.Sprintf(" pattern:%s", prop.Pattern)
		}
		result[name] = desc
	}
	return result, nil
}

func containsStr(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
