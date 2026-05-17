package portal

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// handleListPublishedAPIs returns a paginated list of published APIs available
// in the developer portal. Supports filtering by category, tags, and status.
//
// Query parameters:
//   - offset: pagination offset (default 0)
//   - limit: page size (default 25, max 100)
//   - category: filter by API category
//   - tags: comma-separated list of tags
//   - status: filter by lifecycle status
//   - provider: filter by API provider
//   - version: filter by API version
func (s *Server) handleListPublishedAPIs(c *gin.Context) {
	ctx := c.Request.Context()
	offset, limit := parsePagination(c)

	filter := &models.APIFilter{
		Category: c.Query("category"),
		Status:   c.Query("status"),
		Provider: c.Query("provider"),
		Version:  c.Query("version"),
		Tenant:   getTenant(c),
	}

	if tags := c.Query("tags"); tags != "" {
		filter.Tags = parseCommaSeparated(tags)
	}

	apis, total, err := s.store.ListPublishedAPIs(ctx, offset, limit, filter)
	if err != nil {
		s.logger.Error("failed to list published APIs", "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to retrieve API catalog",
			Code:      "CATALOG_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.PaginatedResponse[models.PublishedAPI]{
		Data:       apis,
		Total:      total,
		Offset:     offset,
		Limit:      limit,
		Count:      int64(len(apis)),
		RequestID:  c.GetString("request_id"),
	})
}

// handleGetAPIDetails returns detailed information about a specific published API
// including its OpenAPI/Swagger definition, documentation links, and endpoints.
func (s *Server) handleGetAPIDetails(c *gin.Context) {
	ctx := c.Request.Context()
	apiID := c.Param("apiID")

	api, err := s.store.GetAPIDetails(ctx, apiID)
	if err != nil {
		s.logger.Error("failed to get API details", "api_id", apiID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:     "API not found",
			Code:      "API_NOT_FOUND",
			Status:    http.StatusNotFound,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Data:      api,
		RequestID: c.GetString("request_id"),
	})
}

// handleSearchAPIs performs full-text search across API names, descriptions,
// tags, and provider names. Results are relevance-ranked.
//
// Query parameters:
//   - q: search query string (required)
//   - offset: pagination offset
//   - limit: page size
func (s *Server) handleSearchAPIs(c *gin.Context) {
	ctx := c.Request.Context()
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "search query parameter 'q' is required",
			Code:      "INVALID_PARAMETER",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	offset, limit := parsePagination(c)

	apis, total, err := s.store.SearchAPIs(ctx, query, offset, limit)
	if err != nil {
		s.logger.Error("failed to search APIs", "query", query, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "search operation failed",
			Code:      "SEARCH_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.PaginatedResponse[models.PublishedAPI]{
		Data:       apis,
		Total:      total,
		Offset:     offset,
		Limit:      limit,
		Count:      int64(len(apis)),
		RequestID:  c.GetString("request_id"),
	})
}

// handleGetAPIRating returns the aggregated rating (average stars and count)
// for a specific API.
func (s *Server) handleGetAPIRating(c *gin.Context) {
	ctx := c.Request.Context()
	apiID := c.Param("apiID")

	rating, err := s.store.GetAPIRating(ctx, apiID)
	if err != nil {
		s.logger.Error("failed to get API rating", "api_id", apiID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:     "API rating not found",
			Code:      "RATING_NOT_FOUND",
			Status:    http.StatusNotFound,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Data:      rating,
		RequestID: c.GetString("request_id"),
	})
}

// handleGetAPIReviews returns paginated user reviews for a specific API.
//
// Query parameters:
//   - offset: pagination offset
//   - limit: page size
func (s *Server) handleGetAPIReviews(c *gin.Context) {
	ctx := c.Request.Context()
	apiID := c.Param("apiID")
	offset, limit := parsePagination(c)

	reviews, total, err := s.store.GetAPIReviews(ctx, apiID, offset, limit)
	if err != nil {
		s.logger.Error("failed to get API reviews", "api_id", apiID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to retrieve reviews",
			Code:      "REVIEWS_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.PaginatedResponse[models.APIReview]{
		Data:       reviews,
		Total:      total,
		Offset:     offset,
		Limit:      limit,
		Count:      int64(len(reviews)),
		RequestID:  c.GetString("request_id"),
	})
}

// handleGetAPIDocumentation returns all documentation entries associated
// with a specific API (Markdown docs, PDFs, how-to guides, etc.).
func (s *Server) handleGetAPIDocumentation(c *gin.Context) {
	ctx := c.Request.Context()
	apiID := c.Param("apiID")

	docs, err := s.store.GetAPIDocumentation(ctx, apiID)
	if err != nil {
		s.logger.Error("failed to get API docs", "api_id", apiID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to retrieve documentation",
			Code:      "DOCS_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Data:      docs,
		RequestID: c.GetString("request_id"),
	})
}

// handleGetAPISwagger serves the OpenAPI/Swagger definition for a specific API.
// The definition is used by the Try-it console and for client SDK generation.
func (s *Server) handleGetAPISwagger(c *gin.Context) {
	ctx := c.Request.Context()
	apiID := c.Param("apiID")

	swagger, err := s.store.GetAPISwagger(ctx, apiID)
	if err != nil {
		s.logger.Error("failed to get swagger", "api_id", apiID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:     "swagger definition not found",
			Code:      "SWAGGER_NOT_FOUND",
			Status:    http.StatusNotFound,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, swagger)
}

// handleGetAPIThumbnail returns the API thumbnail image.
func (s *Server) handleGetAPIThumbnail(c *gin.Context) {
	// Thumbnails are served as redirect to object storage or binary data.
	// For now, return a placeholder redirect.
	apiID := c.Param("apiID")
	c.Redirect(http.StatusFound, "/assets/apis/"+apiID+"/thumbnail.png")
}

// --- Helper functions ---

func parseCommaSeparated(s string) []string {
	var result []string
	start := 0
	for i, ch := range s {
		if ch == ',' {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

func parseInt64(s string, defaultVal int64) int64 {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultVal
	}
	return v
}
