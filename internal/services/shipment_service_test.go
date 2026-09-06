package services

import (
	"context"
	"testing"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/config"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/testutil"
)

func TestShipmentLifecycle(t *testing.T) {
	steps := []models.ShipmentStatus{models.StatusCreated, models.StatusLabelCreated, models.StatusReadyForPickup, models.StatusHandedOver, models.StatusReceivedAtOrigin, models.StatusSorting, models.StatusInTransit, models.StatusArrivedDestinationHub, models.StatusOutForDelivery, models.StatusDelivered}
	for index := 0; index < len(steps)-1; index++ {
		if !steps[index].CanTransitionTo(steps[index+1]) {
			t.Fatalf("%s -> %s should be valid", steps[index], steps[index+1])
		}
	}
	if models.StatusDelivered.CanTransitionTo(models.StatusSorting) {
		t.Fatal("delivered shipment must not return to sorting")
	}
}

func TestReturnLifecycle(t *testing.T) {
	steps := []models.ShipmentStatus{models.StatusReturnRequested, models.StatusReturnAuthorized, models.StatusReturnLabelCreated, models.StatusWaitingCustomerHandover, models.StatusReturnReceivedByShipping, models.StatusReturnPrepared, models.StatusReturnInTransit, models.StatusReturnReceivedAtWarehouse, models.StatusReturnCompleted}
	for index := 0; index < len(steps)-1; index++ {
		if !steps[index].CanTransitionTo(steps[index+1]) {
			t.Fatalf("%s -> %s should be valid", steps[index], steps[index+1])
		}
	}
}

func TestGenerateNumber(t *testing.T) {
	first, err := generateNumber("NS-DE")
	if err != nil {
		t.Fatalf("generate first tracking number: %v", err)
	}
	second, err := generateNumber("NS-DE")
	if err != nil {
		t.Fatalf("generate second tracking number: %v", err)
	}
	if first == second {
		t.Fatal("tracking numbers must be collision resistant")
	}
}

func TestCreateShipmentIsIdempotent(t *testing.T) {
	database := testutil.NewTestDB(t)
	service := NewShipmentService(database, config.Warehouse{Name: "NordShop", Street: "Musterstrasse", HouseNumber: "10", PostalCode: "35039", City: "Marburg", CountryCode: "DE"})
	from := time.Now().UTC().Add(2 * time.Hour)
	until := from.Add(2 * time.Hour)
	input := CreateShipmentInput{OrderID: "17", CustomerID: "5", HandoverCode: "PHE-HO-DE-20260906-7K4M9P", Carrier: "DHL", ServiceLevel: "standard", EstimatedFrom: &from, EstimatedUntil: &until, Recipient: models.AddressSnapshot{FirstName: "Mustafa", LastName: "Oezdemir", Street: "Musterstrasse", HouseNumber: "25", PostalCode: "35037", City: "Marburg", CountryCode: "DE"}, Items: []ItemInput{{ProductID: "12", Name: "Product Name", SKU: "ABC-123", Quantity: 1}}}
	first, created, err := service.Create(context.Background(), input, "idem-17", "ecommerce-gin", "req-17")
	if err != nil {
		t.Fatalf("create first shipment: %v", err)
	}
	if !created {
		t.Fatal("first shipment should be marked created")
	}
	if first.Status != models.StatusHandedOver {
		t.Fatalf("e-commerce handover must enter the waiting-for-receipt queue, got %s", first.Status)
	}
	second, created, err := service.Create(context.Background(), input, "idem-17", "ecommerce-gin", "req-17")
	if err != nil {
		t.Fatalf("replay shipment creation: %v", err)
	}
	if created {
		t.Fatal("replayed shipment should not be marked created")
	}
	if first.PublicID != second.PublicID || first.TrackingNumber != second.TrackingNumber {
		t.Fatal("replayed shipment should return the existing shipment")
	}
	var count int64
	if err := database.Model(&models.Shipment{}).Count(&count).Error; err != nil {
		t.Fatalf("count shipments: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 shipment, got %d", count)
	}
	var events int64
	if err := database.Model(&models.ShipmentEvent{}).Where("shipment_id = ? AND status = ?", first.ID, models.StatusHandedOver).Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("expected one handed-over event, got %d: %v", events, err)
	}
}

func TestCreateShipmentRejectsInvalidGermanAddress(t *testing.T) {
	database := testutil.NewTestDB(t)
	service := NewShipmentService(database, config.Warehouse{Name: "NordShop", Street: "Musterstrasse", HouseNumber: "10", PostalCode: "35039", City: "Marburg", CountryCode: "DE"})
	_, _, err := service.Create(context.Background(), CreateShipmentInput{OrderID: "17", CustomerID: "5", HandoverCode: "PHE-HO-DE-20260906-7K4M9P", Carrier: "DHL", ServiceLevel: "standard", Recipient: models.AddressSnapshot{FirstName: "Mustafa", LastName: "Oezdemir", Street: "Musterstrasse", HouseNumber: "25", PostalCode: "3503", City: "Marburg", CountryCode: "DE"}, Items: []ItemInput{{ProductID: "12", Name: "Product Name", Quantity: 1}}}, "idem-17", "ecommerce-gin", "req-17")
	if err == nil {
		t.Fatal("expected invalid German address to be rejected")
	}
}

func TestCreateReturnUsesPublicShipmentIdentifier(t *testing.T) {
	database := testutil.NewTestDB(t)
	service := NewShipmentService(database, config.Warehouse{Name: "NordShop", Street: "Musterstrasse", HouseNumber: "10", PostalCode: "35039", City: "Marburg", CountryCode: "DE"})
	shipment, _, err := service.Create(context.Background(), CreateShipmentInput{OrderID: "17", CustomerID: "5", HandoverCode: "PHE-HO-DE-20260906-7K4M9P", Carrier: "DHL", ServiceLevel: "standard", Recipient: models.AddressSnapshot{FirstName: "Mustafa", LastName: "Oezdemir", Street: "Musterstrasse", HouseNumber: "25", PostalCode: "35037", City: "Marburg", CountryCode: "DE"}, Items: []ItemInput{{ProductID: "12", Name: "Product Name", Quantity: 1}}}, "idem-17", "ecommerce-gin", "req-17")
	if err != nil {
		t.Fatalf("create outbound shipment: %v", err)
	}

	returnShipment, created, err := service.CreateReturn(context.Background(), CreateReturnInput{OriginalShipmentID: shipment.PublicID, OrderID: "17", CustomerID: "5", CustomerAddress: models.AddressSnapshot{FirstName: "Mustafa", LastName: "Oezdemir", Street: "Musterstrasse", HouseNumber: "25", PostalCode: "35037", City: "Marburg", CountryCode: "DE"}, Items: []ItemInput{{ProductID: "12", Name: "Product Name", Quantity: 1}}}, "idem-ret-17", "ecommerce-gin", "req-ret-17")
	if err != nil {
		t.Fatalf("create return shipment: %v", err)
	}
	if !created || !returnShipment.IsReturn {
		t.Fatal("return shipment should be created and marked as return")
	}
}

func TestCancelShipmentIsIdempotentBeforeReceipt(t *testing.T) {
	database := testutil.NewTestDB(t)
	service := NewShipmentService(database, config.Warehouse{Name: "NordShop", Street: "Musterstrasse", HouseNumber: "10", PostalCode: "35039", City: "Marburg", CountryCode: "DE"})
	shipment, _, err := service.Create(context.Background(), CreateShipmentInput{OrderID: "17", CustomerID: "5", HandoverCode: "PHE-HO-DE-20260906-7K4M9P", Carrier: "DHL", ServiceLevel: "standard", Recipient: models.AddressSnapshot{FirstName: "Mustafa", LastName: "Oezdemir", Street: "Musterstrasse", HouseNumber: "25", PostalCode: "35037", City: "Marburg", CountryCode: "DE"}, Items: []ItemInput{{ProductID: "12", Name: "Product Name", Quantity: 1}}}, "idem-17", "ecommerce-gin", "req-17")
	if err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	first, replayed, err := service.Cancel(context.Background(), shipment.PublicID, "cancel-17", "req-cancel-17", "ecommerce-gin")
	if err != nil || replayed || first.Status != models.StatusCancelled {
		t.Fatalf("cancel shipment: shipment=%+v replayed=%t err=%v", first, replayed, err)
	}
	second, replayed, err := service.Cancel(context.Background(), shipment.PublicID, "cancel-17", "req-cancel-17-retry", "ecommerce-gin")
	if err != nil || !replayed || second.PublicID != first.PublicID {
		t.Fatalf("replay cancellation: shipment=%+v replayed=%t err=%v", second, replayed, err)
	}
	var events int64
	if err := database.Model(&models.ShipmentEvent{}).Where("shipment_id = ? AND status = ?", shipment.ID, models.StatusCancelled).Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("expected one cancellation event, got %d: %v", events, err)
	}
}

func TestCancelShipmentStartsReturnToSenderAfterReceipt(t *testing.T) {
	database := testutil.NewTestDB(t)
	service := NewShipmentService(database, config.Warehouse{Name: "NordShop", Street: "Musterstrasse", HouseNumber: "10", PostalCode: "35039", City: "Marburg", CountryCode: "DE"})
	shipment, _, err := service.Create(context.Background(), CreateShipmentInput{OrderID: "18", CustomerID: "5", HandoverCode: "PHE-HO-DE-20260906-8K4M9P", Carrier: "DHL", ServiceLevel: "standard", Recipient: models.AddressSnapshot{FirstName: "Mustafa", LastName: "Oezdemir", Street: "Musterstrasse", HouseNumber: "25", PostalCode: "35037", City: "Marburg", CountryCode: "DE"}, Items: []ItemInput{{ProductID: "12", Name: "Product Name", Quantity: 1}}}, "idem-18", "ecommerce-gin", "req-18")
	if err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	shipment, _, err = service.Transition(context.Background(), shipment.PublicID, models.StatusAwaitingReceipt, models.StatusReceivedByShipping, models.RoleAdmin, "1", "received-18", "req-received-18", "shipping-portal")
	if err != nil {
		t.Fatalf("receive shipment: %v", err)
	}
	shipment, replayed, err := service.Cancel(context.Background(), shipment.PublicID, "cancel-18", "req-cancel-18", "ecommerce-gin")
	if err != nil || replayed || shipment.Status != models.StatusReturnToSenderRequested {
		t.Fatalf("expected return-to-sender request, shipment=%+v replayed=%t err=%v", shipment, replayed, err)
	}
}
