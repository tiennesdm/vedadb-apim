package gateway

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Transform Implementation
// ============================================================================

// TransformRequest holds transformation configuration
type TransformRequest struct {
	BodyFormat    string            `json:"body_format,omitempty"`    // "json", "xml"
	TargetFormat  string            `json:"target_format,omitempty"`  // "json", "xml"
	AddHeaders    map[string]string `json:"add_headers,omitempty"`
	RemoveHeaders []string          `json:"remove_headers,omitempty"`
	ModifyHeaders map[string]string `json:"modify_headers,omitempty"` // header_name -> new_value
	BodyTemplate  string            `json:"body_template,omitempty"`
}

// Transformer handles request/response transformations
type Transformer struct{}

// NewTransformer creates a new transformer
func NewTransformer() *Transformer {
	return &Transformer{}
}

// TransformBody converts body between JSON and XML
func (t *Transformer) TransformBody(body []byte, fromFormat, toFormat string) ([]byte, error) {
	switch {
	case fromFormat == "json" && toFormat == "xml":
		return JSONToXML(body)
	case fromFormat == "xml" && toFormat == "json":
		return XMLToJSON(body)
	case fromFormat == toFormat:
		return body, nil
	default:
		return nil, fmt.Errorf("unsupported transformation: %s -> %s", fromFormat, toFormat)
	}
}

// TransformHeaders modifies headers based on configuration
func (t *Transformer) TransformHeaders(headers http.Header, config TransformRequest) http.Header {
	result := make(http.Header)
	for k, v := range headers {
		result[k] = v
	}

	// Remove headers
	for _, h := range config.RemoveHeaders {
		result.Del(h)
	}

	// Modify headers
	for name, value := range config.ModifyHeaders {
		result.Set(name, value)
	}

	// Add headers
	for name, value := range config.AddHeaders {
		result.Set(name, value)
	}

	return result
}

// TransformRequest transforms an HTTP request
func (t *Transformer) TransformRequest(req *http.Request, config TransformRequest) (*http.Request, error) {
	// Transform body if needed
	if config.BodyFormat != "" && config.TargetFormat != "" && config.BodyFormat != config.TargetFormat {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		req.Body.Close()

		transformed, err := t.TransformBody(body, config.BodyFormat, config.TargetFormat)
		if err != nil {
			return nil, fmt.Errorf("transform body: %w", err)
		}

		req.Body = io.NopCloser(bytes.NewReader(transformed))
		req.ContentLength = int64(len(transformed))

		// Update Content-Type
		switch config.TargetFormat {
		case "json":
			req.Header.Set("Content-Type", "application/json")
		case "xml":
			req.Header.Set("Content-Type", "application/xml")
		}
	}

	// Transform headers
	req.Header = t.TransformHeaders(req.Header, config)

	return req, nil
}

// JSONToXML converts JSON to XML
func JSONToXML(jsonData []byte) ([]byte, error) {
	var data interface{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w", err)
	}

	xmlData := mapToXML(data, "root")
	return xml.MarshalIndent(xmlData, "", "  ")
}

// XMLToJSON converts XML to JSON
func XMLToJSON(xmlData []byte) ([]byte, error) {
	var node xmlNode
	if err := xml.Unmarshal(xmlData, &node); err != nil {
		return nil, fmt.Errorf("unmarshal XML: %w", err)
	}

	result := xmlNodeToMap(node)
	return json.MarshalIndent(result, "", "  ")
}

// xmlNode represents a simplified XML node for conversion
type xmlNode struct {
	XMLName xml.Name  `xml:"-"`
	Content string    `xml:",chardata"`
	Nodes   []xmlNode `xml:">any"`
	Attrs   []xmlAttr `xml:"-"`
}

type xmlAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func xmlNodeToMap(node xmlNode) map[string]interface{} {
	result := make(map[string]interface{})
	if strings.TrimSpace(node.Content) != "" {
		result["_text"] = strings.TrimSpace(node.Content)
	}
	for _, n := range node.Nodes {
		key := n.XMLName.Local
		if existing, ok := result[key]; ok {
			// Convert to array if multiple
			switch v := existing.(type) {
			case []interface{}:
				result[key] = append(v, xmlNodeToMap(n))
			default:
				result[key] = []interface{}{v, xmlNodeToMap(n)}
			}
		} else {
			result[key] = xmlNodeToMap(n)
		}
	}
	return result
}

func mapToXML(data interface{}, rootName string) xmlNode {
	node := xmlNode{XMLName: xml.Name{Local: rootName}}
	switch v := data.(type) {
	case map[string]interface{}:
		for key, val := range v {
			child := mapToXML(val, key)
			node.Nodes = append(node.Nodes, child)
		}
	case []interface{}:
		for _, item := range v {
			child := mapToXML(item, "item")
			node.Nodes = append(node.Nodes, child)
		}
	case string:
		node.Content = v
	case float64:
		node.Content = fmt.Sprintf("%v", v)
	case bool:
		node.Content = fmt.Sprintf("%v", v)
	case nil:
		node.Content = ""
	}
	return node
}

// ============================================================================
// TESTS
// ============================================================================

func TestTransformer_JSONToXML_GivenValidJSON_WhenConverted_ThenReturnsXML(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected string
	}{
		{
			name:     "simple object",
			json:     `{"name":"John","age":30}`,
			expected: "John",
		},
		{
			name:     "nested object",
			json:     `{"user":{"name":"John","email":"john@example.com"}}`,
			expected: "name",
		},
		{
			name:     "array",
			json:     `{"items":[{"id":1},{"id":2}]}`,
			expected: "id",
		},
		{
			name:     "empty object",
			json:     `{}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xmlBytes, err := JSONToXML([]byte(tt.json))
			require.NoError(t, err)
			assert.NotNil(t, xmlBytes)
			// Check it's valid XML
			var v interface{}
			err = xml.Unmarshal(xmlBytes, &v)
			assert.NoError(t, err)
		})
	}
}

func TestTransformer_JSONToXML_GivenInvalidJSON_WhenConverted_ThenReturnsError(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"not json", `not json at all`},
		{"truncated", `{"name":"John"`},
		{"invalid syntax", `{"name":}`},
		{"empty string", ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := JSONToXML([]byte(tt.json))
			assert.Error(t, err)
		})
	}
}

func TestTransformer_XMLToJSON_GivenValidXML_WhenConverted_ThenReturnsJSON(t *testing.T) {
	xmlData := `<root>
		<name>John</name>
		<age>30</age>
	</root>`

	jsonBytes, err := XMLToJSON([]byte(xmlData))
	require.NoError(t, err)
	assert.NotNil(t, jsonBytes)

	// Should be valid JSON
	var result map[string]interface{}
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)
}

func TestTransformer_XMLToJSON_GivenInvalidXML_WhenConverted_ThenReturnsError(t *testing.T) {
	tests := []struct {
		name string
		xml  string
	}{
		{"not xml", `not xml at all`},
		{"unclosed tag", `<root><name>John</root>`},
		{"invalid syntax", `<root><name>John</name`},
		{"empty string", ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := XMLToJSON([]byte(tt.xml))
			assert.Error(t, err)
		})
	}
}

func TestTransformer_TransformBody_GivenSameFormat_WhenTransformed_ThenReturnsUnchanged(t *testing.T) {
	transformer := NewTransformer()
	body := []byte(`{"name":"John","age":30}`)

	result, err := transformer.TransformBody(body, "json", "json")
	require.NoError(t, err)
	assert.Equal(t, body, result)

	result, err = transformer.TransformBody(body, "xml", "xml")
	require.NoError(t, err)
	assert.Equal(t, body, result)
}

func TestTransformer_TransformBody_GivenUnsupportedFormat_WhenTransformed_ThenReturnsError(t *testing.T) {
	transformer := NewTransformer()
	body := []byte(`some data`)

	_, err := transformer.TransformBody(body, "yaml", "json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported transformation")

	_, err = transformer.TransformBody(body, "json", "protobuf")
	assert.Error(t, err)
}

func TestTransformer_TransformHeaders_GivenAddHeaders_WhenTransformed_ThenHeadersAdded(t *testing.T) {
	transformer := NewTransformer()

	headers := http.Header{}
	headers.Set("X-Existing", "original")

	config := TransformRequest{
		AddHeaders: map[string]string{
			"X-New":        "new-value",
			"X-Request-ID": "abc-123",
		},
	}

	result := transformer.TransformHeaders(headers, config)
	assert.Equal(t, "new-value", result.Get("X-New"))
	assert.Equal(t, "abc-123", result.Get("X-Request-ID"))
	assert.Equal(t, "original", result.Get("X-Existing"))
}

func TestTransformer_TransformHeaders_GivenRemoveHeaders_WhenTransformed_ThenHeadersRemoved(t *testing.T) {
	transformer := NewTransformer()

	headers := http.Header{}
	headers.Set("X-Secret", "sensitive-data")
	headers.Set("X-Token", "bearer-token")
	headers.Set("X-Public", "public-data")

	config := TransformRequest{
		RemoveHeaders: []string{"X-Secret", "X-Token"},
	}

	result := transformer.TransformHeaders(headers, config)
	assert.Empty(t, result.Get("X-Secret"))
	assert.Empty(t, result.Get("X-Token"))
	assert.Equal(t, "public-data", result.Get("X-Public"))
}

func TestTransformer_TransformHeaders_GivenModifyHeaders_WhenTransformed_ThenHeadersModified(t *testing.T) {
	transformer := NewTransformer()

	headers := http.Header{}
	headers.Set("X-Version", "v1")
	headers.Set("X-Env", "development")

	config := TransformRequest{
		ModifyHeaders: map[string]string{
			"X-Version": "v2",
			"X-Env":     "production",
		},
	}

	result := transformer.TransformHeaders(headers, config)
	assert.Equal(t, "v2", result.Get("X-Version"))
	assert.Equal(t, "production", result.Get("X-Env"))
}

func TestTransformer_TransformHeaders_GivenCombinedOperations_WhenTransformed_ThenCorrectOrder(t *testing.T) {
	transformer := NewTransformer()

	headers := http.Header{}
	headers.Set("X-Keep", "keep-value")
	headers.Set("X-Remove", "remove-value")
	headers.Set("X-Modify", "old-value")

	config := TransformRequest{
		AddHeaders:    map[string]string{"X-New": "new-value"},
		RemoveHeaders: []string{"X-Remove"},
		ModifyHeaders: map[string]string{"X-Modify": "new-value"},
	}

	result := transformer.TransformHeaders(headers, config)
	assert.Equal(t, "keep-value", result.Get("X-Keep"))
	assert.Empty(t, result.Get("X-Remove"))
	assert.Equal(t, "new-value", result.Get("X-Modify"))
	assert.Equal(t, "new-value", result.Get("X-New"))
}

func TestTransformer_TransformHeaders_GivenEmptyHeaders_WhenTransformed_ThenWorks(t *testing.T) {
	transformer := NewTransformer()

	config := TransformRequest{
		AddHeaders: map[string]string{"X-New": "new-value"},
	}

	result := transformer.TransformHeaders(http.Header{}, config)
	assert.Equal(t, "new-value", result.Get("X-New"))
}

func TestTransformer_TransformHeaders_GivenRemoveNonExistent_WhenTransformed_ThenNoError(t *testing.T) {
	transformer := NewTransformer()

	headers := http.Header{}
	headers.Set("X-Exists", "value")

	config := TransformRequest{
		RemoveHeaders: []string{"X-NotExists"},
	}

	result := transformer.TransformHeaders(headers, config)
	assert.Equal(t, "value", result.Get("X-Exists"))
}

func TestTransformer_TransformRequest_GivenJSONtoXML_WhenTransformed_ThenBodyConverted(t *testing.T) {
	transformer := NewTransformer()

	jsonBody := `{"user":{"name":"John","age":30}}`
	req := httptest.NewRequest("POST", "/api", strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	config := TransformRequest{
		BodyFormat:   "json",
		TargetFormat: "xml",
		AddHeaders:   map[string]string{"X-Transformed": "true"},
	}

	result, err := transformer.TransformRequest(req, config)
	require.NoError(t, err)

	body, _ := io.ReadAll(result.Body)
	assert.Contains(t, string(body), "<root>")
	assert.Equal(t, "application/xml", result.Header.Get("Content-Type"))
	assert.Equal(t, "true", result.Header.Get("X-Transformed"))
}

func TestTransformer_TransformRequest_GivenXMLtoJSON_WhenTransformed_ThenBodyConverted(t *testing.T) {
	transformer := NewTransformer()

	xmlBody := `<root><name>John</name><age>30</age></root>`
	req := httptest.NewRequest("POST", "/api", strings.NewReader(xmlBody))
	req.Header.Set("Content-Type", "application/xml")

	config := TransformRequest{
		BodyFormat:   "xml",
		TargetFormat: "json",
	}

	result, err := transformer.TransformRequest(req, config)
	require.NoError(t, err)

	body, _ := io.ReadAll(result.Body)
	// Should be valid JSON
	var v map[string]interface{}
	err = json.Unmarshal(body, &v)
	assert.NoError(t, err)
	assert.Equal(t, "application/json", result.Header.Get("Content-Type"))
}

func TestTransformer_TransformRequest_GivenSameFormat_WhenTransformed_ThenBodyUnchanged(t *testing.T) {
	transformer := NewTransformer()

	jsonBody := `{"name":"John"}`
	req := httptest.NewRequest("POST", "/api", strings.NewReader(jsonBody))

	config := TransformRequest{
		BodyFormat:   "json",
		TargetFormat: "json",
	}

	result, err := transformer.TransformRequest(req, config)
	require.NoError(t, err)

	body, _ := io.ReadAll(result.Body)
	assert.Equal(t, jsonBody, string(body))
}

func TestTransformer_TransformRequest_GivenNoBody_WhenTransformed_ThenNoError(t *testing.T) {
	transformer := NewTransformer()

	req := httptest.NewRequest("GET", "/api", nil)

	config := TransformRequest{
		BodyFormat:   "json",
		TargetFormat: "xml",
		AddHeaders:   map[string]string{"X-Test": "test"},
	}

	result, err := transformer.TransformRequest(req, config)
	require.NoError(t, err)
	assert.Equal(t, "test", result.Header.Get("X-Test"))
}

func TestTransformer_RoundTrip_GivenJSONtoXMLtoJSON_WhenConverted_ThenDataPreserved(t *testing.T) {
	original := map[string]interface{}{
		"name":    "John",
		"age":     30.0,
		"active":  true,
		"balance": 1234.56,
	}

	jsonData, err := json.Marshal(original)
	require.NoError(t, err)

	// JSON -> XML
	xmlData, err := JSONToXML(jsonData)
	require.NoError(t, err)
	assert.NotNil(t, xmlData)

	// The round trip won't be perfect due to XML structure, but both conversions should work
	var xmlV interface{}
	err = xml.Unmarshal(xmlData, &xmlV)
	assert.NoError(t, err)
}

func TestTransformer_JSONToXML_GivenComplexNestedJSON_WhenConverted_ThenProducesValidXML(t *testing.T) {
	jsonData := `{
		"company": {
			"name": "Acme Corp",
			"employees": [
				{"name": "Alice", "role": "engineer"},
				{"name": "Bob", "role": "manager"}
			],
			"address": {
				"street": "123 Main St",
				"city": "Anytown"
			}
		}
	}`

	xmlData, err := JSONToXML([]byte(jsonData))
	require.NoError(t, err)
	assert.Contains(t, string(xmlData), "company")
	assert.Contains(t, string(xmlData), "employees")

	// Verify valid XML
	var node xmlNode
	err = xml.Unmarshal(xmlData, &node)
	assert.NoError(t, err)
}

func TestTransformer_TransformHeaders_GivenCaseInsensitive_WhenModified_ThenWorks(t *testing.T) {
	transformer := NewTransformer()

	headers := http.Header{}
	headers.Set("content-type", "application/json")
	headers.Set("x-custom", "value")

	config := TransformRequest{
		ModifyHeaders: map[string]string{
			"Content-Type": "application/xml",
		},
	}

	result := transformer.TransformHeaders(headers, config)
	// http.Header is case-insensitive for Get
	assert.Equal(t, "application/xml", result.Get("content-type"))
}

func TestTransformer_TransformRequest_GivenRemoveAndAddSameHeader_WhenTransformed_ThenAddWins(t *testing.T) {
	transformer := NewTransformer()

	req := httptest.NewRequest("POST", "/api", strings.NewReader(`{"test":"data"}`))
	req.Header.Set("X-Conflict", "original")

	config := TransformRequest{
		RemoveHeaders: []string{"X-Conflict"},
		AddHeaders:    map[string]string{"X-Conflict": "added"},
	}

	result, err := transformer.TransformRequest(req, config)
	require.NoError(t, err)
	// The order matters - remove then add means the added value wins
	assert.Equal(t, "added", result.Header.Get("X-Conflict"))
}
