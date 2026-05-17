// Package mock provides realistic mock data generation from OpenAPI schemas.
package mock

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math/rand"
	"net/mail"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MockDataGenerator generates realistic mock data from schemas and parameter definitions.
type MockDataGenerator struct {
	// CustomProviders allows overriding generation for specific field names.
	CustomProviders map[string]func() interface{}
	// Seed allows deterministic generation. 0 = random.
	Seed int64

	rng *rand.Rand
}

// NewMockDataGenerator creates a new mock data generator.
func NewMockDataGenerator() *MockDataGenerator {
	g := &MockDataGenerator{
		CustomProviders: make(map[string]string), // Correct type later
		Seed:            0,
	}
	g.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	g.setupDefaultProviders()
	return g
}

// setupDefaultProviders configures built-in field name detectors.
// Smart field detection: id->uuid, email->valid email, etc.
func (g *MockDataGenerator) setupDefaultProviders() {
	// Create proper typed map
	g.CustomProviders = make(map[string]func() interface{})

	// ID / UUID fields
	g.CustomProviders["id"] = func() interface{} { return uuid.New().String() }
	g.CustomProviders["uuid"] = func() interface{} { return uuid.New().String() }
	g.CustomProviders["guid"] = func() interface{} { return uuid.New().String() }
	g.CustomProviders["user_id"] = func() interface{} { return uuid.New().String() }
	g.CustomProviders["api_id"] = func() interface{} { return "api-" + g.randomHex(8) }
	g.CustomProviders["app_id"] = func() interface{} { return "app-" + g.randomHex(8) }

	// Email fields
	g.CustomProviders["email"] = func() interface{} { return g.randomEmail() }
	g.CustomProviders["email_address"] = func() interface{} { return g.randomEmail() }
	g.CustomProviders["user_email"] = func() interface{} { return g.randomEmail() }
	g.CustomProviders["contact_email"] = func() interface{} { return g.randomEmail() }

	// Name fields
	g.CustomProviders["name"] = func() interface{} { return g.randomName() }
	g.CustomProviders["first_name"] = func() interface{} { return g.randomFirstName() }
	g.CustomProviders["last_name"] = func() interface{} { return g.randomLastName() }
	g.CustomProviders["full_name"] = func() interface{} { return g.randomName() }
	g.CustomProviders["username"] = func() interface{} { return g.randomUsername() }

	// Date/Time fields
	g.CustomProviders["created_at"] = func() interface{} { return time.Now().Add(-g.randomDuration()).Format(time.RFC3339) }
	g.CustomProviders["updated_at"] = func() interface{} { return time.Now().Add(-g.randomDuration()).Format(time.RFC3339) }
	g.CustomProviders["published_at"] = func() interface{} { return time.Now().Add(-g.randomDuration()).Format(time.RFC3339) }
	g.CustomProviders["date"] = func() interface{} { return time.Now().Add(-g.randomDuration()).Format("2006-01-02") }
	g.CustomProviders["timestamp"] = func() interface{} { return time.Now().Unix() }
	g.CustomProviders["expiry"] = func() interface{} { return time.Now().Add(24 * time.Hour).Format(time.RFC3339) }

	// Status fields
	g.CustomProviders["status"] = func() interface{} { return g.randomChoice([]string{"active", "inactive", "pending", "approved", "rejected"}) }
	g.CustomProviders["state"] = func() interface{} { return g.randomChoice([]string{"CREATED", "PUBLISHED", "DEPRECATED", "RETIRED"}) }

	// URL fields
	g.CustomProviders["url"] = func() interface{} { return "https://api.vedadata.com/v2/resource/" + g.randomHex(8) }
	g.CustomProviders["href"] = func() interface{} { return "https://api.vedadata.com/v2/resource/" + g.randomHex(8) }
	g.CustomProviders["link"] = func() interface{} { return "https://docs.vedadata.com/apis/" + g.randomHex(6) }
	g.CustomProviders["icon"] = func() interface{} { return "https://cdn.vedadata.com/icons/" + g.randomHex(6) + ".png" }

	// Phone
	g.CustomProviders["phone"] = func() interface{} { return g.randomPhone() }
	g.CustomProviders["phone_number"] = func() interface{} { return g.randomPhone() }

	// Address
	g.CustomProviders["address"] = func() interface{} { return g.randomAddress() }
	g.CustomProviders["city"] = func() interface{} { return g.randomChoice([]string{"San Francisco", "New York", "London", "Berlin", "Tokyo", "Sydney"}) }
	g.CustomProviders["country"] = func() interface{} { return g.randomChoice([]string{"US", "GB", "DE", "JP", "AU", "FR", "CA"}) }
}

// GenerateFromSchema generates mock data from a JSON schema map.
func (g *MockDataGenerator) GenerateFromSchema(schema map[string]interface{}) interface{} {
	if schema == nil {
		return map[string]interface{}{}
	}

	typeVal, _ := schema["type"].(string)
	if typeVal == "" {
		// Check for $ref
		if ref, ok := schema["$ref"].(string); ok {
			return map[string]interface{}{"$ref": ref}
		}
		typeVal = "object"
	}

	switch typeVal {
	case "object":
		return g.generateObject(schema)
	case "array":
		return g.generateArray(schema)
	case "string":
		return g.generateString(schema, "")
	case "integer":
		return g.generateInteger(schema)
	case "number":
		return g.generateNumber(schema)
	case "boolean":
		return g.rng.Intn(2) == 0
	default:
		return nil
	}
}

// GenerateFromParameters generates mock data from parameter definitions.
func (g *MockDataGenerator) GenerateFromParameters(params []ParameterDef) interface{} {
	result := make(map[string]interface{})
	for _, param := range params {
		if param.In != "body" && param.In != "formData" {
			continue
		}
		result[param.Name] = g.generateFromParamType(param.Type, param.Name)
	}
	return result
}

// GenerateJSON generates mock data and returns it as JSON bytes.
func (g *MockDataGenerator) GenerateJSON(schema map[string]interface{}) ([]byte, error) {
	data := g.GenerateFromSchema(schema)
	return json.MarshalIndent(data, "", "  ")
}

// GenerateXML generates mock data and returns it as XML bytes.
// The schema should have an XML element name via the "xml" field or root key.
func (g *MockDataGenerator) GenerateXML(schema map[string]interface{}, rootElement string) ([]byte, error) {
	data := g.GenerateFromSchema(schema)
	if rootElement == "" {
		rootElement = "response"
	}

	xmlData := mapToXMLStruct(data, rootElement)
	return xml.MarshalIndent(xmlData, "", "  ")
}

// --- Internal generators ---

func (g *MockDataGenerator) generateObject(schema map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	if props, ok := schema["properties"].(map[string]interface{}); ok {
		for fieldName, fieldSchema := range props {
			// Check custom providers first
			if provider, ok := g.CustomProviders[fieldName]; ok {
				result[fieldName] = provider()
				continue
			}

			if fsm, ok := fieldSchema.(map[string]interface{}); ok {
				result[fieldName] = g.GenerateFromSchema(fsm)
			} else {
				result[fieldName] = g.generateFromParamType("string", fieldName)
			}
		}
	}

	return result
}

func (g *MockDataGenerator) generateArray(schema map[string]interface{}) []interface{} {
	if items, ok := schema["items"].(map[string]interface{}); ok {
		count := 1 + g.rng.Intn(5) // 1 to 5 items
		result := make([]interface{}, count)
		for i := 0; i < count; i++ {
			result[i] = g.GenerateFromSchema(items)
		}
		return result
	}
	return []interface{}{}
}

func (g *MockDataGenerator) generateString(schema map[string]interface{}, fieldName string) string {
	if provider, ok := g.CustomProviders[fieldName]; ok {
		if s, ok := provider().(string); ok {
			return s
		}
	}

	// Check format
	format, _ := schema["format"].(string)
	switch format {
	case "date":
		return time.Now().Add(-time.Duration(g.rng.Intn(365)) * 24 * time.Hour).Format("2006-01-02")
	case "date-time":
		return time.Now().Add(-g.randomDuration()).Format(time.RFC3339)
	case "email":
		return g.randomEmail()
	case "uuid":
		return uuid.New().String()
	case "uri", "url":
		return "https://api.vedadata.com/resource/" + g.randomHex(8)
	case "password":
		return "P@ssw" + g.randomString(8) + "!"
	case "byte":
		return base64Encode(g.randomString(16))
	case "binary":
		return base64Encode(g.randomString(32))
	}

	// Check enum
	if enum, ok := schema["enum"].([]interface{}); ok && len(enum) > 0 {
		return fmt.Sprintf("%v", enum[g.rng.Intn(len(enum))])
	}

	// Generate based on field name
	switch {
	case strings.Contains(fieldName, "id"):
		return g.randomHex(12)
	case strings.Contains(fieldName, "email"):
		return g.randomEmail()
	case strings.Contains(fieldName, "name"):
		return g.randomName()
	case strings.Contains(fieldName, "description"):
		return g.randomSentence()
	case strings.Contains(fieldName, "status"):
		return g.randomChoice([]string{"active", "inactive", "pending"})
	case strings.Contains(fieldName, "url") || strings.Contains(fieldName, "link"):
		return "https://api.vedadata.com/" + g.randomHex(6)
	case strings.Contains(fieldName, "color"):
		return g.randomColor()
	case strings.Contains(fieldName, "image"):
		return "https://cdn.vedadata.com/images/" + g.randomHex(8) + ".jpg"
	default:
		return g.randomString(8 + g.rng.Intn(20))
	}
}

func (g *MockDataGenerator) generateInteger(schema map[string]interface{}) int64 {
	minimum := int64(0)
	maximum := int64(1000)

	if min, ok := schema["minimum"]; ok {
		switch v := min.(type) {
		case float64:
			minimum = int64(v)
		case int64:
			minimum = v
		}
	}
	if max, ok := schema["maximum"]; ok {
		switch v := max.(type) {
		case float64:
			maximum = int64(v)
		case int64:
			maximum = v
		}
	}

	range_ := maximum - minimum
	if range_ <= 0 {
		range_ = 1000
	}
	return minimum + int64(g.rng.Int63n(range_))
}

func (g *MockDataGenerator) generateNumber(schema map[string]interface{}) float64 {
	min := 0.0
	max := 1000.0

	if mn, ok := schema["minimum"].(float64); ok {
		min = mn
	}
	if mx, ok := schema["maximum"].(float64); ok {
		max = mx
	}

	return min + g.rng.Float64()*(max-min)
}

func (g *MockDataGenerator) generateFromParamType(paramType, name string) interface{} {
	switch paramType {
	case "string":
		if provider, ok := g.CustomProviders[name]; ok {
			return provider()
		}
		return g.randomString(8)
	case "integer", "int":
		return int64(g.rng.Intn(1000))
	case "number", "float":
		return g.rng.Float64() * 1000
	case "boolean":
		return g.rng.Intn(2) == 0
	case "array":
		return []string{g.randomString(4), g.randomString(4)}
	default:
		return g.randomString(8)
	}
}

// --- Random data generators ---

var firstNames = []string{
	"James", "Mary", "Robert", "Patricia", "John", "Jennifer", "Michael", "Linda",
	"David", "Elizabeth", "William", "Barbara", "Richard", "Susan", "Joseph", "Jessica",
	"Thomas", "Sarah", "Charles", "Karen", "Christopher", "Nancy", "Daniel", "Lisa",
	"Ava", "Liam", "Olivia", "Noah", "Emma", "Oliver", "Charlotte", "Elijah", "Amelia",
	"Sophia", "Lucas", "Isabella", "Mason", "Mia", "Ethan", "Evelyn",
}

var lastNames = []string{
	"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis",
	"Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson",
	"Thomas", "Taylor", "Moore", "Jackson", "Martin", "Lee", "Perez", "Thompson", "White",
	"Harris", "Sanchez", "Clark", "Ramirez", "Lewis", "Robinson", "Walker", "Young",
}

var domains = []string{
	"vedadata.com", "example.com", "acme.io", "test.org", "demo.net",
	"apidomain.com", "myapp.io", "sandbox.dev",
}

func (g *MockDataGenerator) randomEmail() string {
	name := strings.ToLower(g.randomString(6) + "." + g.randomString(6))
	domain := domains[g.rng.Intn(len(domains))]
	return name + "@" + domain
}

func (g *MockDataGenerator) randomName() string {
	return g.randomFirstName() + " " + g.randomLastName()
}

func (g *MockDataGenerator) randomFirstName() string {
	return firstNames[g.rng.Intn(len(firstNames))]
}

func (g *MockDataGenerator) randomLastName() string {
	return lastNames[g.rng.Intn(len(lastNames))]
}

func (g *MockDataGenerator) randomUsername() string {
	adjectives := []string{"happy", "brave", "clever", "swift", "bright", "cool", "mighty", "sneaky"}
	nouns := []string{"fox", "eagle", "wolf", "bear", "tiger", "shark", "lion", "hawk"}
	adj := adjectives[g.rng.Intn(len(adjectives))]
	noun := nouns[g.rng.Intn(len(nouns))]
	num := g.rng.Intn(999)
	return fmt.Sprintf("%s_%s_%d", adj, noun, num)
}

func (g *MockDataGenerator) randomPhone() string {
	area := 200 + g.rng.Intn(800)
	prefix := 200 + g.rng.Intn(800)
	line := g.rng.Intn(10000)
	return fmt.Sprintf("+1-%03d-%03d-%04d", area, prefix, line)
}

func (g *MockDataGenerator) randomAddress() string {
	streets := []string{"Main St", "Oak Ave", "Park Rd", "Maple Dr", "Cedar Ln", "Elm St"}
	cities := []string{"Springfield", "Riverside", "Madison", "Georgetown", "Fairview"}
	return fmt.Sprintf("%d %s, %s, CA %05d",
		g.rng.Intn(9999)+1,
		streets[g.rng.Intn(len(streets))],
		cities[g.rng.Intn(len(cities))],
		g.rng.Intn(90000)+10000,
	)
}

func (g *MockDataGenerator) randomColor() string {
	colors := []string{"#3498db", "#e74c3c", "#2ecc71", "#f39c12", "#9b59b6", "#1abc9c", "#34495e", "#e67e22"}
	return colors[g.rng.Intn(len(colors))]
}

func (g *MockDataGenerator) randomSentence() string {
	words := []string{"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit",
		"sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore", "et", "dolore", "magna", "aliqua"}
	count := 5 + g.rng.Intn(10)
	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = words[g.rng.Intn(len(words))]
	}
	return strings.Title(strings.Join(result, " ")) + "."
}

func (g *MockDataGenerator) randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[g.rng.Intn(len(charset))]
	}
	return string(b)
}

func (g *MockDataGenerator) randomHex(length int) string {
	const hexChars = "0123456789abcdef"
	b := make([]byte, length)
	for i := range b {
		b[i] = hexChars[g.rng.Intn(len(hexChars))]
	}
	return string(b)
}

func (g *MockDataGenerator) randomChoice(choices []string) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[g.rng.Intn(len(choices))]
}

func (g *MockDataGenerator) randomDuration() time.Duration {
	return time.Duration(g.rng.Intn(365*24)) * time.Hour
}

func base64Encode(s string) string {
	return "b64:" + s // Simplified for mock purposes
}

// mapToXMLStruct converts a map to a structure that can be marshaled to XML.
func mapToXMLStruct(data interface{}, rootTag string) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, val := range v {
			result[key] = mapToXMLStruct(val, key)
		}
		return struct {
			XMLName string      `xml:"` + rootTag + `"`
			Data    interface{} `xml:",any"`
		}{
			Data: result,
		}
	default:
		return v
	}
}

// GenerateMockData is the main entry point for generating mock data.
// It supports JSON and XML formats.
func (g *MockDataGenerator) GenerateMockData(schema map[string]interface{}, format string, rootElement string) (interface{}, []byte, error) {
	data := g.GenerateFromSchema(schema)

	switch strings.ToLower(format) {
	case "xml":
		bytes, err := g.GenerateXML(schema, rootElement)
		return data, bytes, err
	case "json", "":
		bytes, err := json.MarshalIndent(data, "", "  ")
		return data, bytes, err
	default:
		bytes, err := json.MarshalIndent(data, "", "  ")
		return data, bytes, err
	}
}

// SetSeed sets the random seed for deterministic generation.
func (g *MockDataGenerator) SetSeed(seed int64) {
	g.Seed = seed
	g.rng = rand.New(rand.NewSource(seed))
}

// IsValidEmail checks if a string is a valid email address.
func IsValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// GenerateSampleData generates sample data for a given field name and type.
// This is the smart field detection system.
func (g *MockDataGenerator) GenerateSampleData(fieldName, fieldType string) interface{} {
	fieldName = strings.ToLower(fieldName)

	// Check custom providers first
	if provider, ok := g.CustomProviders[fieldName]; ok {
		return provider()
	}

	// Type-based generation
	switch strings.ToLower(fieldType) {
	case "string":
		return g.generateStringForField(fieldName)
	case "integer", "int", "int64":
		return g.generateInteger(nil)
	case "number", "float", "float64":
		return g.generateNumber(nil)
	case "boolean":
		return g.rng.Intn(2) == 0
	case "array":
		return []interface{}{g.randomString(4), g.randomString(4)}
	case "date":
		return time.Now().Add(-time.Duration(g.rng.Intn(365)) * 24 * time.Hour).Format("2006-01-02")
	case "datetime":
		return time.Now().Add(-g.randomDuration()).Format(time.RFC3339)
	case "uuid":
		return uuid.New().String()
	case "email":
		return g.randomEmail()
	default:
		return g.generateStringForField(fieldName)
	}
}

func (g *MockDataGenerator) generateStringForField(fieldName string) string {
	// Smart field detection patterns
	switch {
	case strings.Contains(fieldName, "email"):
		return g.randomEmail()
	case strings.HasSuffix(fieldName, "_id") || fieldName == "id":
		return uuid.New().String()
	case strings.Contains(fieldName, "uuid") || strings.Contains(fieldName, "guid"):
		return uuid.New().String()
	case strings.Contains(fieldName, "name"):
		return g.randomName()
	case strings.Contains(fieldName, "url") || strings.Contains(fieldName, "link") || strings.Contains(fieldName, "href"):
		return "https://api.vedadata.com/resource/" + g.randomHex(8)
	case strings.Contains(fieldName, "date") || strings.Contains(fieldName, "time"):
		return time.Now().Format(time.RFC3339)
	case strings.Contains(fieldName, "phone"):
		return g.randomPhone()
	case strings.Contains(fieldName, "address"):
		return g.randomAddress()
	case strings.Contains(fieldName, "color"):
		return g.randomColor()
	case strings.Contains(fieldName, "status"):
		return g.randomChoice([]string{"active", "inactive", "pending", "approved", "rejected"})
	case strings.Contains(fieldName, "description") || strings.Contains(fieldName, "note") || strings.Contains(fieldName, "comment"):
		return g.randomSentence()
	default:
		return g.randomString(8 + g.rng.Intn(20))
	}
}

// DeepCopy creates a deep copy of a map[string]interface{}.
func DeepCopy(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case map[string]interface{}:
			result[k] = DeepCopy(val)
		default:
			result[k] = reflect.ValueOf(v).Interface()
		}
	}
	return result
}
