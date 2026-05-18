// Package audit provides the Audit Log HTTP API for querying and managing
// audit log entries with pagination and filtering capabilities.
package audit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vedadb/vapim/internal/auth"
	"github.com/vedadb/vapim/pkg/models"
)

// QueryStore defines the interface needed by the audit API for querying logs.
type QueryStore interface {
	GetAuditLogs(tenantID string, limit, offset int) ([]*models.AuditLogDB, int, error)
	RawQuery(query string, args ...interface{}) ([][]byte, error)
}

// API provides HTTP handlers for the audit log endpoints.
type API struct {
	store QueryStore
}

// NewAPI creates a new audit log API handler.
func NewAPI(store QueryStore) *API {
	return &API{store: store}
}

// QueryRequest represents the query parameters for filtering audit logs.
type QueryRequest struct {
	EntityType string    `form:"entity_type"`
	Action     string    `form:"action"`
	UserID     string    `form:"user_id"`
	ResourceID string    `form:"resource_id"`
	DateFrom   time.Time `form:"date_from"`
	DateTo     time.Time `form:"date_to"`
	Limit      int       `form:"limit,default=20"`
	Offset     int       `form:"offset,default=0"`
}

// RegisterRoutes registers the audit log API routes on the given Gin router group.
func (a *API) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/audit-logs", auth.AdminOnly(), a.ListAuditLogs)
	r.GET("/audit-logs/:id", auth.AdminOnly(), a.GetAuditLog)
}

// ListAuditLogs handles GET /audit-logs with pagination and filtering.
//
// Query Parameters:
//   - entity_type: Filter by resource type (e.g., "api", "application", "subscription")
//   - action: Filter by action (e.g., "CREATE", "UPDATE", "DELETE")
//   - user_id: Filter by user who performed the action
//   - resource_id: Filter by the affected resource ID
//   - date_from: Start date (RFC3339 format)
//   - date_to: End date (RFC3339 format)
//   - limit: Maximum number of results (default 20, max 100)
//   - offset: Pagination offset (default 0)
//
// Response:
//
//	{
//	  "data": [...],
//	  "total": 150,
//	  "limit": 20,
//	  "offset": 0,
//	  "has_more": true
//	}
func (a *API) ListAuditLogs(c *gin.Context) {
	req := QueryRequest{
		Limit:  20,
		Offset: 0,
	}
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query parameters: " + err.Error()})
		return
	}

	// Validate and clamp limit
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > 100 {
		req.Limit = 100
	}
	if req.Offset < 0 {
		req.Offset = 0
	}

	// Get tenantID from context
	tenantID, _ := c.Get("tenantID").(string)
	if tenantID == "" {
		tenantID = "carbon.super"
	}

	// Build the query with filters
	logs, total, err := a.queryWithFilters(tenantID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query audit logs: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":     logs,
		"total":    total,
		"limit":    req.Limit,
		"offset":   req.Offset,
		"has_more": (req.Offset + len(logs)) < total,
	})
}

// GetAuditLog handles GET /audit-logs/:id to retrieve a single audit log entry.
func (a *API) GetAuditLog(c *gin.Context) {
	id := c.Param("id")

	rows, err := a.store.RawQuery("SELECT * FROM audit_log WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch audit log: " + err.Error()})
		return
	}
	if len(rows) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "audit log not found"})
		return
	}

	// Parse the JSON row from VedaDB wire protocol
	var log models.AuditLogDB
	if err := json.Unmarshal(rows[0], &log); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse audit log: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, log)
}



// queryWithFilters builds and executes a filtered audit log query.
func (a *API) queryWithFilters(tenantID string, req QueryRequest) ([]*models.AuditLogDB, int, error) {
	// Build WHERE clauses
	var conditions []string
	var args []interface{}

	conditions = append(conditions, "tenant_id = ?")
	args = append(args, tenantID)

	if req.EntityType != "" {
		conditions = append(conditions, "resource_type = ?")
		args = append(args, req.EntityType)
	}
	if req.Action != "" {
		conditions = append(conditions, "action LIKE ?")
		args = append(args, "%"+req.Action+"%")
	}
	if req.UserID != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, req.UserID)
	}
	if req.ResourceID != "" {
		conditions = append(conditions, "resource_id = ?")
		args = append(args, req.ResourceID)
	}
	if !req.DateFrom.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, req.DateFrom.Format(time.RFC3339))
	}
	if !req.DateTo.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, req.DateTo.Format(time.RFC3339))
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Get total count
	var total int
	countRows, err := a.store.RawQuery("SELECT COUNT(*) as total FROM audit_log "+whereClause, args...)
	if err == nil && len(countRows) > 0 {
		// Parse count result
		var countResult struct {
			Total int `json:"total"`
		}
		if parseErr := unmarshalJSON(countRows[0], &countResult); parseErr == nil {
			total = countResult.Total
		}
	}

	// Build the main query with LIMIT and OFFSET
	// Use RawQuery with the full query
	limit := strconv.Itoa(req.Limit)
	offset := strconv.Itoa(req.Offset)

	query := "SELECT * FROM audit_log " + whereClause + " ORDER BY timestamp DESC LIMIT " + limit + " OFFSET " + offset
	rows, err := a.store.RawQuery(query, args...)
	if err != nil {
		return nil, 0, err
	}

	logs := make([]*models.AuditLogDB, 0, len(rows))
	for _, row := range rows {
		var log models.AuditLogDB
		if err := unmarshalJSON(row, &log); err != nil {
			continue // Skip unparsable rows
		}
		logs = append(logs, &log)
	}

	return logs, total, nil
}

// unmarshalJSON parses JSON bytes into a value using encoding/json.
func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
