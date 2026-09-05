package services

import (
	"testing"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
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
	steps := []models.ShipmentStatus{models.StatusReturnRequested, models.StatusReturnAuthorized, models.StatusReturnLabelCreated, models.StatusReturnInTransit, models.StatusReturnReceived, models.StatusReturnCompleted}
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
