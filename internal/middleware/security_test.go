package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersEnablesHSTSInProduction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(SecurityHeaders(true))
	router.GET("/health", func(context *gin.Context) { context.Status(http.StatusOK) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Header().Get("Strict-Transport-Security") == "" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers missing: %v", response.Header())
	}
}
