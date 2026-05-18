// Package gateway provides the complete middleware chain for the VedaDB API Manager Gateway.
package gateway

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Response capture writer
// ---------------------------------------------------------------------------

// captureWriter intercepts the response body so we can transform it after
// the handler has written JSON.
type captureWriter struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func newCaptureWriter(w gin.ResponseWriter) *captureWriter {
	return &captureWriter{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		status:         http.StatusOK,
	}
}

func (w *captureWriter) WriteHeader(code int) {
	w.status = code
	// Don't write header to underlying writer yet; we may change content-type.
}

func (w *captureWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	return w.body.WriteString(s)
}

// ---------------------------------------------------------------------------
// FormatMiddleware
// ---------------------------------------------------------------------------

// FormatMiddleware converts JSON responses to XML or CSV based on the
// Accept header or the ?format= query parameter.
func FormatMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		format := c.Query("format")
		if format == "" {
			accept := c.GetHeader("Accept")
			switch {
			case strings.Contains(accept, "application/xml"):
				format = "xml"
			case strings.Contains(accept, "text/csv"):
				format = "csv"
			}
		}

		if format != "xml" && format != "csv" {
			c.Next()
			return
		}

		// Capture the JSON response written by downstream handlers.
		cw := newCaptureWriter(c.Writer)
		c.Writer = cw
		c.Next()

		// Only transform successful JSON responses.
		if cw.status >= 200 && cw.status < 300 && cw.body.Len() > 0 {
			if format == "xml" {
				convertResponseToXML(c, cw)
			} else if format == "csv" {
				convertResponseToCSV(c, cw)
			}
		} else {
			// Pass through unchanged for errors or empty bodies.
			cw.ResponseWriter.WriteHeader(cw.status)
			io.Copy(cw.ResponseWriter, cw.body)
		}
	}
}

// ---------------------------------------------------------------------------
// XML conversion
// ---------------------------------------------------------------------------

// xmlEnvelope wraps any JSON payload in a generic XML envelope.
type xmlEnvelope struct {
	XMLName xml.Name    `xml:"response"`
	Status  int         `xml:"status"`
	Data    interface{} `xml:"data"`
}

// xmlItem is a single key-value pair for XML map encoding.
type xmlItem struct {
	XMLName xml.Name `xml:"item"`
	Key     string   `xml:"key,attr"`
	Value   string   `xml:"value,attr"`
}

// xmlItems is a slice of xmlItem used for map serialization.
type xmlItems struct {
	Items []xmlItem `xml:"item"`
}

func convertResponseToXML(c *gin.Context, cw *captureWriter) {
	body := cw.body.Bytes()

	// Try to unmarshal as a generic map first.
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		// If it's not a JSON object, try as raw string.
		cw.ResponseWriter.Header().Set("Content-Type", "application/xml; charset=utf-8")
		cw.ResponseWriter.WriteHeader(cw.status)
		env := xmlEnvelope{Status: cw.status, Data: string(body)}
		out, _ := xml.MarshalIndent(env, "", "  ")
		cw.ResponseWriter.Write([]byte(xml.Header))
		cw.ResponseWriter.Write(out)
		return
	}

	// Convert the map to XML-friendly structure.
	xmlData := mapToXMLItems(payload)

	cw.ResponseWriter.Header().Set("Content-Type", "application/xml; charset=utf-8")
	cw.ResponseWriter.WriteHeader(cw.status)
	cw.ResponseWriter.Write([]byte(xml.Header))

	enc := xml.NewEncoder(cw.ResponseWriter)
	enc.Indent("", "  ")
	enc.Encode(xmlData)
}

// mapToXMLItems recursively converts a map[string]interface{} into a structure
// that can be marshaled to XML. It handles nested maps and slices.
func mapToXMLItems(data map[string]interface{}) xmlItems {
	items := xmlItems{}
	for k, v := range data {
		items.Items = append(items.Items, xmlItem{
			Key:   k,
			Value: fmt.Sprintf("%v", v),
		})
	}
	return items
}

// ---------------------------------------------------------------------------
// CSV conversion
// ---------------------------------------------------------------------------

func convertResponseToCSV(c *gin.Context, cw *captureWriter) {
	body := cw.body.Bytes()

	// Try to extract a "rows" or "data" array from common response shapes.
	var rows []map[string]interface{}

	// Try direct array.
	if err := json.Unmarshal(body, &rows); err == nil && len(rows) > 0 {
		writeCSV(c, cw, rows)
		return
	}

	// Try { "data": [ ... ] } wrapper (PaginatedResponse / APIResponse).
	var wrapped struct {
		Data interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Data != nil {
		dataBytes, _ := json.Marshal(wrapped.Data)
		if err := json.Unmarshal(dataBytes, &rows); err == nil && len(rows) > 0 {
			writeCSV(c, cw, rows)
			return
		}
	}

	// Try { "apis": [ ... ] } wrapper (APIListResponse).
	var apiList struct {
		APIs []map[string]interface{} `json:"apis"`
	}
	if err := json.Unmarshal(body, &apiList); err == nil && len(apiList.APIs) > 0 {
		writeCSV(c, cw, apiList.APIs)
		return
	}

	// Fallback: write the original JSON if we can't extract rows.
	cw.ResponseWriter.Header().Set("Content-Type", "application/json")
	cw.ResponseWriter.WriteHeader(cw.status)
	io.Copy(cw.ResponseWriter, cw.body)
}

// writeCSV writes a slice of map[string]interface{} as CSV to the response.
func writeCSV(c *gin.Context, cw *captureWriter, rows []map[string]interface{}) {
	// Collect all unique headers from the first row.
	var headers []string
	if len(rows) > 0 {
		for k := range rows[0] {
			headers = append(headers, k)
		}
	}

	cw.ResponseWriter.Header().Set("Content-Type", "text/csv; charset=utf-8")
	cw.ResponseWriter.Header().Set("Content-Disposition", "attachment; filename=\"export.csv\"")
	cw.ResponseWriter.WriteHeader(cw.status)

	w := csv.NewWriter(cw.ResponseWriter)
	// Write headers.
	if err := w.Write(headers); err != nil {
		return
	}
	// Write rows.
	for _, row := range rows {
		record := make([]string, len(headers))
		for i, h := range headers {
			if v, ok := row[h]; ok {
				record[i] = fmt.Sprintf("%v", v)
			}
		}
		if err := w.Write(record); err != nil {
			return
		}
	}
	w.Flush()
}
