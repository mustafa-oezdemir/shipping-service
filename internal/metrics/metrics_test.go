package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/testutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestStateCollectorExportsAggregatesWithoutSensitiveData(t *testing.T) {
	database := testutil.NewTestDB(t)
	now := time.Now().UTC().Add(-2 * time.Minute)
	outbound := models.Shipment{PublicID: "shp_secret", ShipmentNumber: "SHP-1", TrackingNumber: "customer-tracking-secret", ExternalOrderID: "42", ExternalCustomerID: "customer@example.com", BusinessKey: "outbound:42", SourceService: "ecommerce-gin", IdempotencyKey: "secret-api-token", Status: models.StatusInTransit, Carrier: "carrier", ServiceLevel: "standard"}
	returned := models.Shipment{PublicID: "ret_secret", ShipmentNumber: "RET-1", TrackingNumber: "qr-token-secret", ExternalOrderID: "42", ExternalCustomerID: "customer@example.com", BusinessKey: "return:42", SourceService: "ecommerce-gin", IdempotencyKey: "return-key", IsReturn: true, Status: models.StatusReturnInTransit, Carrier: "carrier", ServiceLevel: "standard"}
	if err := database.Create(&outbound).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&returned).Error; err != nil {
		t.Fatal(err)
	}
	event := models.ShipmentEvent{EventID: "evt-1", ShipmentID: outbound.ID, SourceService: "test", IdempotencyKey: "event-1", RequestID: "req-1", EventType: "status_changed", Status: models.StatusInTransit, Title: "In transit", OccurredAt: now, CreatedByType: "test"}
	if err := database.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	outbox := models.OutboxEvent{EventID: "out-1", ShipmentID: outbound.ID, ShipmentEventID: event.ID, RequestID: "req-1", EventType: "status_changed", Payload: `{"authorization":"Bearer secret-api-token","password":"secret"}`, Status: models.OutboxStatusPending, NextAttemptAt: now}
	if err := database.Create(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	database.Model(&outbox).Update("created_at", now)

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewStateCollector(database))
	response := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		`shipping_shipments_current{shipment_type="outbound",status="in_transit"} 1`,
		`shipping_returns_current{status="return_in_transit"} 1`,
		`shipping_outbox_current{status="pending"} 1`,
		"shipping_outbox_oldest_pending_seconds",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q in metrics: %s", expected, body)
		}
	}
	for _, forbidden := range []string{"secret-api-token", "customer@example.com", "customer-tracking-secret", "qr-token-secret", "Bearer", "password"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("sensitive value %q leaked into metrics", forbidden)
		}
	}
}
