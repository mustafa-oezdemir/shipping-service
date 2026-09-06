package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/config"
	"github.com/mustafa-oezdemir/shipping-service/internal/middleware"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"github.com/mustafa-oezdemir/shipping-service/internal/testutil"
)

func TestCreateShipmentRequiresServiceToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newTestRouter(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", bytes.NewBufferString(`{"order_id":"17"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected request id header")
	}
	if bytes.Contains(response.Body.Bytes(), []byte("test-service-token")) {
		t.Fatal("service token must not be exposed in the response")
	}
}

func TestCreateShipmentRejectsInvalidServiceToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newTestRouter(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", bytes.NewBufferString(`{"order_id":"17"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer invalid-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestCreateShipmentRequiresIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newTestRouter(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-service-token-012345678901234567890123")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"INVALID_IDEMPOTENCY_KEY"`)) {
		t.Fatalf("expected structured idempotency error, got %d: %s", response.Code, response.Body.String())
	}
}

func TestCreateShipmentRejectsOversizedAndUnknownJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newTestRouter(t)
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "oversized", body: `{"order_id":"` + strings.Repeat("x", int(maxInternalJSONBytes)) + `"}`, wantStatus: http.StatusRequestEntityTooLarge, wantCode: "REQUEST_TOO_LARGE"},
		{name: "unknown field", body: `{"order_id":"17","unexpected":true}`, wantStatus: http.StatusBadRequest, wantCode: "INVALID_REQUEST"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer test-service-token-012345678901234567890123")
			request.Header.Set("Idempotency-Key", "idem-json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+test.wantCode+`"`)) {
				t.Fatalf("expected %d/%s, got %d: %s", test.wantStatus, test.wantCode, response.Code, response.Body.String())
			}
		})
	}
}

func TestCreateShipmentReturnsVersionedJSONContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newTestRouter(t)
	body := map[string]any{"order_id": "17", "customer_id": "5", "carrier": "DHL", "service_level": "standard", "recipient": map[string]any{"first_name": "Mustafa", "last_name": "Oezdemir", "street": "Musterstrasse", "house_number": "25", "postal_code": "35037", "city": "Marburg", "country_code": "DE"}, "items": []map[string]any{{"product_id": "12", "name": "Product Name", "quantity": 1}}}
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-service-token-012345678901234567890123")
	request.Header.Set("Idempotency-Key", "idem-17")
	request.Header.Set("X-Request-ID", "req-17")
	request.Header.Set("X-Service-Name", "ecommerce-gin")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Request-ID") != "req-17" {
		t.Fatal("expected request id to be propagated")
	}
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope["success"] != true {
		t.Fatalf("expected success envelope, got %v", envelope)
	}
	data := envelope["data"].(map[string]any)
	if data["shipment_id"] == "" || data["tracking_number"] == "" || data["status"] != "created" {
		t.Fatalf("unexpected shipment payload: %v", data)
	}
	if _, exists := data["recipient"]; exists {
		t.Fatal("create shipment response must not expose recipient PII")
	}
}

func TestCreateShipmentIdempotentReplayReturnsSameShipment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newTestRouter(t)
	body := map[string]any{"order_id": "17", "customer_id": "5", "carrier": "DHL", "service_level": "standard", "recipient": map[string]any{"first_name": "Mustafa", "last_name": "Oezdemir", "street": "Musterstrasse", "house_number": "25", "postal_code": "35037", "city": "Marburg", "country_code": "DE"}, "items": []map[string]any{{"product_id": "12", "name": "Product Name", "quantity": 1}}}
	payload, _ := json.Marshal(body)
	first := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", bytes.NewReader(payload))
	first.Header.Set("Content-Type", "application/json")
	first.Header.Set("Authorization", "Bearer test-service-token-012345678901234567890123")
	first.Header.Set("Idempotency-Key", "idem-replay")
	first.Header.Set("X-Service-Name", "ecommerce-gin")
	firstResponse := httptest.NewRecorder()
	router.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("expected first create 201, got %d", firstResponse.Code)
	}

	second := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", bytes.NewReader(payload))
	second.Header.Set("Content-Type", "application/json")
	second.Header.Set("Authorization", "Bearer test-service-token-012345678901234567890123")
	second.Header.Set("Idempotency-Key", "idem-replay")
	second.Header.Set("X-Service-Name", "ecommerce-gin")
	secondResponse := httptest.NewRecorder()
	router.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("expected replay 200, got %d", secondResponse.Code)
	}
	var firstEnvelope, secondEnvelope map[string]any
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &firstEnvelope); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if firstEnvelope["data"].(map[string]any)["shipment_id"] != secondEnvelope["data"].(map[string]any)["shipment_id"] {
		t.Fatal("expected idempotent replay to return the same shipment id")
	}
	if secondEnvelope["data"].(map[string]any)["idempotent_replay"] != true {
		t.Fatal("expected replay response to be flagged")
	}
}

func TestCreateShipmentRejectsIdempotencyKeyReuseForDifferentOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newTestRouter(t)
	firstBody := map[string]any{"order_id": "17", "customer_id": "5", "carrier": "DHL", "service_level": "standard", "recipient": map[string]any{"first_name": "Mustafa", "last_name": "Oezdemir", "street": "Musterstrasse", "house_number": "25", "postal_code": "35037", "city": "Marburg", "country_code": "DE"}, "items": []map[string]any{{"product_id": "12", "name": "Product Name", "quantity": 1}}}
	secondBody := map[string]any{"order_id": "18", "customer_id": "5", "carrier": "DHL", "service_level": "standard", "recipient": map[string]any{"first_name": "Mustafa", "last_name": "Oezdemir", "street": "Musterstrasse", "house_number": "25", "postal_code": "35037", "city": "Marburg", "country_code": "DE"}, "items": []map[string]any{{"product_id": "12", "name": "Product Name", "quantity": 1}}}

	requestShipment := func(body map[string]any) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(body)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer test-service-token-012345678901234567890123")
		request.Header.Set("Idempotency-Key", "idem-reused")
		request.Header.Set("X-Service-Name", "ecommerce-gin")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	if response := requestShipment(firstBody); response.Code != http.StatusCreated {
		t.Fatalf("expected first create 201, got %d: %s", response.Code, response.Body.String())
	}
	response := requestShipment(secondBody)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected conflicting key reuse 409, got %d: %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"code":"IDEMPOTENCY_CONFLICT"`)) {
		t.Fatalf("expected structured idempotency error, got %s", response.Body.String())
	}
}

func TestGetShipmentForOrderOmitsRecipientPII(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newTestRouter(t)
	createShipment(t, router)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/shipments/order/17", nil)
	request.Header.Set("Authorization", "Bearer test-service-token-012345678901234567890123")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("recipient")) || bytes.Contains(response.Body.Bytes(), []byte("street")) {
		t.Fatal("shipment lookup must not expose recipient PII")
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("tracking_number")) {
		t.Fatal("shipment lookup should expose tracking data")
	}
}

func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	database := testutil.NewTestDB(t)
	service := services.NewShipmentService(database, config.Warehouse{Name: "NordShop", Street: "Musterstrasse", HouseNumber: "10", PostalCode: "35039", City: "Marburg", CountryCode: "DE"})
	handler := New(service, database, "http://localhost:8090")
	router := gin.New()
	router.Use(middleware.RequestMetadata("shipping-service"))
	internal := router.Group("/api/v1")
	internal.Use(middleware.RequireServiceToken("test-service-token-012345678901234567890123"), middleware.NoStore())
	internal.POST("/shipments", middleware.RequireIdempotencyKey(), handler.CreateShipment)
	internal.GET("/shipments/order/:orderID", handler.GetShipmentForOrder)
	internal.GET("/shipments/:trackingNumber", handler.GetShipmentByTracking)
	internal.GET("/returns/:trackingNumber", handler.GetReturnByTracking)
	operations := router.Group("")
	operations.Use(middleware.RequireServiceToken("test-service-token-012345678901234567890123"), middleware.RequireRole(models.RoleShippingAdmin), middleware.NoStore())
	operations.PATCH("/api/v1/shipments/:id/status", middleware.RequireIdempotencyKey(), handler.TransitionShipment)
	return router
}

func createShipment(t *testing.T, router *gin.Engine) {
	t.Helper()
	body := map[string]any{"order_id": "17", "customer_id": "5", "carrier": "DHL", "service_level": "standard", "recipient": map[string]any{"first_name": "Mustafa", "last_name": "Oezdemir", "street": "Musterstrasse", "house_number": "25", "postal_code": "35037", "city": "Marburg", "country_code": "DE"}, "items": []map[string]any{{"product_id": "12", "name": "Product Name", "quantity": 1}}}
	payload, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/shipments", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-service-token-012345678901234567890123")
	request.Header.Set("Idempotency-Key", "idem-17")
	request.Header.Set("X-Service-Name", "ecommerce-gin")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated && response.Code != http.StatusOK {
		t.Fatalf("create shipment returned %d: %s", response.Code, response.Body.String())
	}
}
