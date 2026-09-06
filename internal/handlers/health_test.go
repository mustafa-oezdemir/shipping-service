package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	appmetrics "github.com/mustafa-oezdemir/shipping-service/internal/metrics"
	"github.com/mustafa-oezdemir/shipping-service/internal/testutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestHealthAndReadiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewTestDB(t)
	registry := prometheus.NewRegistry()
	metrics := appmetrics.New(registry)
	handler := New(nil, database, "http://localhost:8090", metrics)
	router := gin.New()
	router.GET("/health/live", handler.Health)
	router.GET("/health/ready", handler.Ready)

	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
	metricsResponse := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if body := metricsResponse.Body.String(); !strings.Contains(body, "shipping_health_live 1") || !strings.Contains(body, "shipping_health_ready 1") {
		t.Fatalf("health metrics missing: %s", body)
	}

	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed database readiness status = %d", response.Code)
	}
}
