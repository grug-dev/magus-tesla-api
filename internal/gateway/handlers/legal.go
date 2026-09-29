package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
)

// Privacy serves the short privacy notice at /privacy. Public, static, no module call.
func (h *Handler) Privacy(c *gin.Context) {
	render(c, http.StatusOK, pages.Privacy())
}

// Terms serves the short terms of use at /terms. Public, static, no module call.
func (h *Handler) Terms(c *gin.Context) {
	render(c, http.StatusOK, pages.Terms())
}
