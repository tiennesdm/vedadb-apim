// Package graphql provides the HTTP server and handler for the VedaDB API Manager GraphQL endpoint.
package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/graphql-go/graphql"
	"github.com/vedadb/vapim/internal/audit"
	"github.com/vedadb/vapim/internal/tenant"
	"github.com/vedadb/vapim/internal/webhook"
)

// ServerConfig holds configuration for the GraphQL server.
type ServerConfig struct {
	Store    SQLStore
	Logger   *slog.Logger
	Auditor  audit.AuditLogger
	Events   webhook.EventPublisher
	Endpoint string // GraphQL endpoint path, defaults to "/graphql"
}

// Server encapsulates the GraphQL schema and HTTP handlers.
type Server struct {
	schema   graphql.Schema
	logger   *slog.Logger
	auditor  audit.AuditLogger
	endpoint string
}

// NewServer creates and initializes a new GraphQL server.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "/graphql"
	}
	if cfg.Auditor == nil {
		cfg.Auditor = &audit.NopLogger{}
	}

	resolver := NewResolver(cfg.Store, cfg.Logger, cfg.Auditor, cfg.Events)

	schema, err := buildSchema(resolver)
	if err != nil {
		return nil, fmt.Errorf("build graphql schema: %w", err)
	}

	return &Server{
		schema:   schema,
		logger:   cfg.Logger,
		auditor:  cfg.Auditor,
		endpoint: cfg.Endpoint,
	}, nil
}

// graphQLRequest represents an incoming GraphQL request.
type graphQLRequest struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
	OperationName string                 `json:"operationName,omitempty"`
}

// graphQLResponse represents a GraphQL response.
type graphQLResponse struct {
	Data       json.RawMessage   `json:"data,omitempty"`
	Errors     []graphQLError    `json:"errors,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// graphQLError represents a single GraphQL error.
type graphQLError struct {
	Message    string                 `json:"message"`
	Path       []interface{}          `json:"path,omitempty"`
	Locations  []errorLocation        `json:"locations,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// errorLocation represents the location of an error in the query.
type errorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// RegisterRoutes registers the GraphQL routes on the provided Gin router.
func (s *Server) RegisterRoutes(r gin.IRouter) {
	// GraphQL endpoint
	r.POST(s.endpoint, s.handleGraphQL)
	r.GET(s.endpoint, s.handleGraphQL)

	// GraphQL Playground
	r.GET(s.endpoint+"/playground", s.handlePlayground)
}

// Handler returns the GraphQL handler as a standard http.HandlerFunc.
func (s *Server) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := s.withAuthContext(r.Context(), r)
		s.execute(ctx, w, r)
	}
}

// handleGraphQL is the Gin handler for GraphQL requests.
func (s *Server) handleGraphQL(c *gin.Context) {
	ctx := s.withAuthContext(c.Request.Context(), c.Request)
	s.execute(ctx, c.Writer, c.Request)
}

// execute processes a single GraphQL request.
func (s *Server) execute(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// Parse request
	reqPayload, err := parseRequest(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request", err.Error())
		return
	}

	// Execute GraphQL query
	result := graphql.Do(graphql.Params{
		Schema:         s.schema,
		RequestString:  reqPayload.Query,
		VariableValues: reqPayload.Variables,
		OperationName:  reqPayload.OperationName,
		Context:        ctx,
	})

	// Build response
	resp := graphQLResponse{}

	if result.HasErrors() {
		resp.Errors = make([]graphQLError, 0, len(result.Errors))
		for _, err := range result.Errors {
			gqlErr := graphQLError{
				Message:   err.Message,
				Locations: make([]errorLocation, 0, len(err.Locations)),
				Path:      make([]interface{}, 0, len(err.Path)),
			}
			for _, loc := range err.Locations {
				gqlErr.Locations = append(gqlErr.Locations, errorLocation{
					Line:   loc.Line,
					Column: loc.Column,
				})
			}
			for _, p := range err.Path {
				gqlErr.Path = append(gqlErr.Path, p)
			}
			// Add tenant context to error extensions
			gqlErr.Extensions = map[string]interface{}{
				"tenant_id": tenant.TenantIDFromContext(ctx),
			}
			resp.Errors = append(resp.Errors, gqlErr)
		}
	}

	if result.Data != nil {
		dataBytes, err := json.Marshal(result.Data)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to serialize response", err.Error())
			return
		}
		resp.Data = dataBytes
	}

	// Add extensions
	resp.Extensions = map[string]interface{}{
		"tenant_id": tenant.TenantIDFromContext(ctx),
	}

	// Write response
	w.Header().Set("Content-Type", "application/json")
	if len(resp.Errors) > 0 {
		w.WriteHeader(http.StatusOK) // GraphQL returns 200 even with errors
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(resp); err != nil {
		s.logger.Error("failed to encode graphql response", slog.String("error", err.Error()))
	}
}

// parseRequest reads and parses the GraphQL request from the HTTP body.
func parseRequest(r *http.Request) (*graphQLRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer r.Body.Close()

	if len(body) == 0 {
		// Try to get query from URL parameters for GET requests
		query := r.URL.Query().Get("query")
		if query != "" {
			return &graphQLRequest{
				Query:         query,
				Variables:     parseURLVariables(r),
				OperationName: r.URL.Query().Get("operationName"),
			}, nil
		}
		return nil, fmt.Errorf("empty request body and no query parameter")
	}

	var req graphQLRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("unmarshal body: %w", err)
	}

	if strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	return &req, nil
}

// parseURLVariables parses GraphQL variables from URL query parameters.
func parseURLVariables(r *http.Request) map[string]interface{} {
	varsStr := r.URL.Query().Get("variables")
	if varsStr == "" {
		return nil
	}

	var vars map[string]interface{}
	if err := json.Unmarshal([]byte(varsStr), &vars); err != nil {
		return nil
	}
	return vars
}

// withAuthContext extracts authentication information from the HTTP request
// and injects it into the context for use by resolvers.
func (s *Server) withAuthContext(ctx context.Context, r *http.Request) context.Context {
	// Extract auth token from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token := parts[1]
			// In production, validate the JWT and extract claims
			// For now, we pass the token through for downstream use
			ctx = context.WithValue(ctx, "auth_token", token)
		}
	}

	// Extract user info from headers (set by upstream auth middleware)
	if userID := r.Header.Get("X-User-ID"); userID != "" {
		ctx = context.WithValue(ctx, "user_id", userID)
	}
	if username := r.Header.Get("X-Username"); username != "" {
		ctx = context.WithValue(ctx, "username", username)
	}
	if role := r.Header.Get("X-User-Role"); role != "" {
		ctx = context.WithValue(ctx, "user_role", role)
	}

	// Extract client IP
	clientIP := r.Header.Get("X-Forwarded-For")
	if clientIP == "" {
		clientIP = r.Header.Get("X-Real-IP")
	}
	if clientIP == "" {
		clientIP = strings.Split(r.RemoteAddr, ":")[0]
	}
	ctx = context.WithValue(ctx, "client_ip", clientIP)

	return ctx
}

// writeError writes a JSON error response.
func (s *Server) writeError(w http.ResponseWriter, statusCode int, message, details string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	resp := graphQLResponse{
		Errors: []graphQLError{
			{
				Message: message,
				Extensions: map[string]interface{}{
					"details": details,
				},
			},
		},
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("failed to encode error response", slog.String("error", err.Error()))
	}
}

// ---- Playground ----

// handlePlayground serves the GraphQL Playground HTML page.
func (s *Server) handlePlayground(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, playgroundHTML, s.endpoint)
}

// playgroundHTML is the HTML template for the GraphQL Playground.
const playgroundHTML = `<!DOCTYPE html>
<html>
<head>
    <meta charset=utf-8/>
    <meta name="viewport" content="user-scalable=no, initial-scale=1.0, minimum-scale=1.0, maximum-scale=1.0, minimal-ui">
    <title>VedaDB API Manager - GraphQL Playground</title>
    <link rel="stylesheet" href="//cdn.jsdelivr.net/npm/graphql-playground-react/build/static/css/index.css"/>
    <link rel="shortcut icon" href="//cdn.jsdelivr.net/npm/graphql-playground-react/build/favicon.png"/>
    <script src="//cdn.jsdelivr.net/npm/graphql-playground-react/build/static/js/middleware.js"></script>
    <style type="text/css">
        html { font-family: "Open Sans", sans-serif; overflow: hidden; }
        body { margin: 0; padding: 0; background: #f1f1f1; }
    </style>
</head>
<body>
<div id="root">
    <style>
        body { background-color: rgb(23, 42, 58); font-family: Open Sans, sans-serif; height: 90vh; }
        #root { height: 100%%; width: 100%%; display: flex; align-items: center; justify-content: center; }
        .loading { font-size: 32px; font-weight: 200; color: rgba(255, 255, 255, .6); margin-left: 20px; }
        img { width: 78px; height: 78px; }
        .title { font-weight: 400; }
    </style>
    <img src='//cdn.jsdelivr.net/npm/graphql-playground-react/build/logo.png' alt=''>
    <div class="loading">
        Loading <span class="title">VedaDB API Manager Playground</span>
    </div>
</div>
<script>
    window.addEventListener('load', function (event) {
        GraphQLPlayground.init(document.getElementById('root'), {
            endpoint: '%s',
            settings: {
                'editor.theme': 'dark',
                'editor.cursorShape': 'line',
                'tracing.hideTracingResponse': false,
            }
        })
    })
</script>
</body>
</html>`

// RegisterGinRoutes registers all GraphQL-related routes on a Gin router group.
// This includes the main GraphQL endpoint and the Playground.
func RegisterGinRoutes(r gin.IRouter, store SQLStore, logger *slog.Logger, auditor audit.AuditLogger, events webhook.EventPublisher) error {
	srv, err := NewServer(ServerConfig{
		Store:    store,
		Logger:   logger,
		Auditor:  auditor,
		Events:   events,
		Endpoint: "/graphql",
	})
	if err != nil {
		return fmt.Errorf("create graphql server: %w", err)
	}

	srv.RegisterRoutes(r)
	return nil
}
