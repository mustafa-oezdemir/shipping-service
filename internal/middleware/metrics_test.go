package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	appmetrics "github.com/mustafa-oezdemir/shipping-service/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsUsesRouteTemplateAndRecordsServerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := prometheus.NewRegistry()
	metrics := appmetrics.New(registry)
	router := gin.New()
	router.Use(Metrics(metrics))
	router.GET("/shipments/:id", func(context *gin.Context) { context.Status(http.StatusInternalServerError) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/shipments/secret-tracking-123", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}

	metricsResponse := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	if !strings.Contains(metricsResponse.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("unexpected content type: %s", metricsResponse.Header().Get("Content-Type"))
	}
	if !strings.Contains(body, `shipping_http_requests_total{method="GET",route="/shipments/:id",status="500"} 1`) {
		t.Fatalf("server error metric missing: %s", body)
	}
	if strings.Contains(body, "secret-tracking-123") {
		t.Fatal("raw dynamic path leaked into metrics")
	}
}
