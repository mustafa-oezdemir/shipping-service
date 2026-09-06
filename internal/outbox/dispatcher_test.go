package outbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/config"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"github.com/mustafa-oezdemir/shipping-service/internal/testutil"
)

func TestDispatcherRetriesAndMarksDelivered(t *testing.T) {
	database := testutil.NewTestDB(t)
	service := services.NewShipmentService(database, config.Warehouse{Name: "NordShop", Street: "Musterstrasse", HouseNumber: "10", PostalCode: "35039", City: "Marburg", CountryCode: "DE"})
	shipment, _, err := service.Create(context.Background(), services.CreateShipmentInput{OrderID: "17", CustomerID: "5", Carrier: "DHL", ServiceLevel: "standard", Recipient: models.AddressSnapshot{FirstName: "Mustafa", LastName: "Oezdemir", Street: "Musterstrasse", HouseNumber: "25", PostalCode: "35037", City: "Marburg", CountryCode: "DE"}, Items: []services.ItemInput{{ProductID: "12", Name: "Product Name", Quantity: 1}}}, "idem-17", "ecommerce-gin", "req-17")
	if err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	// Isolate this test to the status callback. Shipment creation correctly
	// creates its own outbox event, which would otherwise consume the server's
	// first (intentional) failure before the status event is delivered.
	if err := database.Where("shipment_id = ?", shipment.ID).Delete(&models.OutboxEvent{}).Error; err != nil {
		t.Fatalf("remove creation outbox event: %v", err)
	}
	_, _, err = service.Transition(context.Background(), shipment.PublicID, models.StatusCreated, models.StatusLabelCreated, models.RoleShippingAdmin, "ops-1", "status-1", "req-status-1", "shipping-service")
	if err != nil {
		t.Fatalf("transition shipment: %v", err)
	}
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer callback-token-012345678901234567890123" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Header.Get("X-Request-ID") == "" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		current := atomic.AddInt32(&attempts, 1)
		if current == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	dispatcher := NewDispatcher(database, Config{CallbackURL: server.URL, CallbackToken: "callback-token-012345678901234567890123", PollInterval: 10 * time.Millisecond, RetryBaseDelay: 5 * time.Millisecond, ProcessingStaleAfter: 50 * time.Millisecond, MaxAttempts: 4, RequestTimeout: time.Second, ServiceName: "shipping-service"})
	if err := dispatcher.DeliverDue(context.Background()); err != nil {
		t.Fatalf("first delivery attempt: %v", err)
	}
	var firstPass models.OutboxEvent
	if err := database.Where("event_type = ?", "status_changed").First(&firstPass).Error; err != nil {
		t.Fatalf("load outbox event: %v", err)
	}
	if firstPass.Status != models.OutboxStatusPending || firstPass.Attempts != 1 {
		t.Fatalf("expected pending retry after first failure, got status=%s attempts=%d", firstPass.Status, firstPass.Attempts)
	}
	if err := database.Model(&models.OutboxEvent{}).Where("id = ?", firstPass.ID).Update("next_attempt_at", time.Now().UTC().Add(-time.Millisecond)).Error; err != nil {
		t.Fatalf("force due retry: %v", err)
	}
	if err := dispatcher.DeliverDue(context.Background()); err != nil {
		t.Fatalf("second delivery attempt: %v", err)
	}
	var delivered models.OutboxEvent
	if err := database.First(&delivered, firstPass.ID).Error; err != nil {
		t.Fatalf("reload delivered outbox event: %v", err)
	}
	if delivered.Status != models.OutboxStatusDelivered {
		t.Fatalf("expected delivered status, got %s", delivered.Status)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected 2 callback attempts, got %d", attempts)
	}
}

func TestDispatcherFailsPermanentAuthorizationErrors(t *testing.T) {
	database := testutil.NewTestDB(t)
	if err := database.Create(&models.OutboxEvent{EventID: "evt_manual", ShipmentID: 1, ShipmentEventID: 1, RequestID: "req_manual", EventType: "status_changed", Payload: `{"event_id":"evt_manual"}`, Status: models.OutboxStatusPending, NextAttemptAt: time.Now().UTC()}).Error; err != nil {
		t.Fatalf("create outbox event: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	dispatcher := NewDispatcher(database, Config{CallbackURL: server.URL, CallbackToken: "callback-token-012345678901234567890123", PollInterval: 10 * time.Millisecond, RetryBaseDelay: 5 * time.Millisecond, ProcessingStaleAfter: 50 * time.Millisecond, MaxAttempts: 4, RequestTimeout: time.Second, ServiceName: "shipping-service"})
	if err := dispatcher.DeliverDue(context.Background()); err != nil {
		t.Fatalf("deliver due: %v", err)
	}
	var failed models.OutboxEvent
	if err := database.Where("event_id = ?", "evt_manual").First(&failed).Error; err != nil {
		t.Fatalf("reload outbox event: %v", err)
	}
	if failed.Status != models.OutboxStatusFailed {
		t.Fatalf("expected failed status, got %s", failed.Status)
	}
	if !strings.Contains(failed.LastError, "401") {
		t.Fatalf("expected permanent 401 failure to be recorded, got %q", failed.LastError)
	}
}
