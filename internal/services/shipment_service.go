package services

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/config"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidShipment   = errors.New("invalid shipment request")
	ErrShipmentNotFound  = errors.New("shipment not found")
	ErrInvalidTransition = errors.New("invalid shipment status transition")
	ErrForbidden         = errors.New("operation is not permitted for this role")
)

type CreateShipmentInput struct {
	OrderID        string                 `json:"order_id"`
	CustomerID     string                 `json:"customer_id"`
	Recipient      models.AddressSnapshot `json:"recipient"`
	Items          []ItemInput            `json:"items"`
	Carrier        string                 `json:"carrier"`
	ServiceLevel   string                 `json:"service_level"`
	EstimatedFrom  *time.Time             `json:"estimated_from"`
	EstimatedUntil *time.Time             `json:"estimated_until"`
}

type ItemInput struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	SKU       string `json:"sku"`
	Quantity  int    `json:"quantity"`
}

type CreateReturnInput struct {
	OriginalShipmentID uint                   `json:"original_shipment_id"`
	OrderID            string                 `json:"order_id"`
	CustomerID         string                 `json:"customer_id"`
	CustomerAddress    models.AddressSnapshot `json:"customer_address"`
	Items              []ItemInput            `json:"items"`
}

type ShipmentService struct {
	database  *gorm.DB
	warehouse config.Warehouse
}

func NewShipmentService(database *gorm.DB, warehouse config.Warehouse) *ShipmentService {
	if database == nil {
		panic("shipping: database is required")
	}
	return &ShipmentService{database: database, warehouse: warehouse}
}

func (service *ShipmentService) Create(ctx context.Context, input CreateShipmentInput, idempotencyKey string) (*models.Shipment, error) {
	if err := validateCreateInput(input, idempotencyKey); err != nil {
		return nil, err
	}
	var shipment models.Shipment
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		err := transaction.Where("idempotency_key = ?", idempotencyKey).First(&shipment).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		shipmentNumber, err := generateNumber("SHP")
		if err != nil {
			return err
		}
		trackingNumber, err := generateNumber("NS-DE")
		if err != nil {
			return err
		}
		shipment = models.Shipment{
			ShipmentNumber: shipmentNumber, TrackingNumber: trackingNumber, ExternalOrderID: input.OrderID,
			ExternalCustomerID: input.CustomerID, IdempotencyKey: idempotencyKey, Status: models.StatusCreated,
			Carrier: input.Carrier, ServiceLevel: input.ServiceLevel, Recipient: input.Recipient,
			Sender: warehouseAddress(service.warehouse), EstimatedFrom: input.EstimatedFrom, EstimatedUntil: input.EstimatedUntil,
		}
		if err := transaction.Create(&shipment).Error; err != nil {
			return fmt.Errorf("create shipment: %w", err)
		}
		for _, item := range input.Items {
			if err := transaction.Create(&models.ShipmentItem{ShipmentID: shipment.ID, ExternalProductID: item.ProductID, Name: item.Name, SKU: item.SKU, Quantity: item.Quantity}).Error; err != nil {
				return fmt.Errorf("create shipment item: %w", err)
			}
		}
		return service.recordEvent(transaction, &shipment, models.StatusCreated, "shipment_created", "Shipment created", "", nil, idempotencyKey+"/created", "ecommerce_service", input.CustomerID)
	})
	if err != nil {
		return nil, err
	}
	return &shipment, nil
}

func (service *ShipmentService) GetPublic(ctx context.Context, trackingNumber string) (*models.Shipment, error) {
	var shipment models.Shipment
	if err := service.database.WithContext(ctx).Preload("Events", func(database *gorm.DB) *gorm.DB {
		return database.Order("occurred_at ASC")
	}).Where("tracking_number = ?", trackingNumber).First(&shipment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrShipmentNotFound
		}
		return nil, err
	}
	shipment.Recipient = models.AddressSnapshot{}
	shipment.Sender = models.AddressSnapshot{}
	return &shipment, nil
}

func (service *ShipmentService) GetInternal(ctx context.Context, shipmentID uint) (*models.Shipment, error) {
	var shipment models.Shipment
	if err := service.database.WithContext(ctx).Preload("Items").Preload("Events", func(database *gorm.DB) *gorm.DB {
		return database.Order("occurred_at ASC")
	}).First(&shipment, shipmentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrShipmentNotFound
		}
		return nil, err
	}
	return &shipment, nil
}

func (service *ShipmentService) CreateReturn(ctx context.Context, input CreateReturnInput, idempotencyKey string) (*models.Shipment, error) {
	if input.OriginalShipmentID == 0 || strings.TrimSpace(input.OrderID) == "" || strings.TrimSpace(input.CustomerID) == "" || strings.TrimSpace(idempotencyKey) == "" || len(input.Items) == 0 {
		return nil, ErrInvalidShipment
	}
	var shipment models.Shipment
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Where("idempotency_key = ?", idempotencyKey).First(&shipment).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var original models.Shipment
		if err := transaction.First(&original, input.OriginalShipmentID).Error; err != nil {
			return ErrShipmentNotFound
		}
		trackingNumber, err := generateNumber("RET-DE")
		if err != nil {
			return err
		}
		shipmentNumber, err := generateNumber("RET")
		if err != nil {
			return err
		}
		shipment = models.Shipment{
			ShipmentNumber: shipmentNumber, TrackingNumber: trackingNumber, ExternalOrderID: input.OrderID, ExternalCustomerID: input.CustomerID,
			IdempotencyKey: idempotencyKey, OriginalShipmentID: &original.ID, IsReturn: true, Status: models.StatusReturnRequested,
			Carrier: original.Carrier, ServiceLevel: original.ServiceLevel, Sender: input.CustomerAddress, Recipient: warehouseAddress(service.warehouse),
		}
		if err := transaction.Create(&shipment).Error; err != nil {
			return err
		}
		for _, item := range input.Items {
			if item.Quantity < 1 || strings.TrimSpace(item.ProductID) == "" || strings.TrimSpace(item.Name) == "" {
				return ErrInvalidShipment
			}
			if err := transaction.Create(&models.ShipmentItem{ShipmentID: shipment.ID, ExternalProductID: item.ProductID, Name: item.Name, SKU: item.SKU, Quantity: item.Quantity}).Error; err != nil {
				return err
			}
		}
		return service.recordEvent(transaction, &shipment, shipment.Status, "return_requested", "Return requested", "", nil, idempotencyKey+"/created", "ecommerce_service", input.CustomerID)
	})
	if err != nil {
		return nil, err
	}
	return &shipment, nil
}

func (service *ShipmentService) Transition(ctx context.Context, shipmentID uint, expected models.ShipmentStatus, next models.ShipmentStatus, actorRole models.Role, actorID, idempotencyKey string) (*models.Shipment, error) {
	if !canManageStatus(actorRole, next) {
		return nil, ErrForbidden
	}
	var shipment models.Shipment
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).First(&shipment, shipmentID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrShipmentNotFound
			}
			return err
		}
		if idempotencyKey != "" {
			var event models.ShipmentEvent
			if err := transaction.Where("idempotency_key = ?", idempotencyKey).First(&event).Error; err == nil {
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if shipment.Status != expected || !shipment.Status.CanTransitionTo(next) {
			return ErrInvalidTransition
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": next, "version": shipment.Version + 1}
		if next == models.StatusDelivered {
			updates["delivered_at"] = now
		}
		result := transaction.Model(&models.Shipment{}).Where("id = ? AND status = ? AND version = ?", shipment.ID, expected, shipment.Version).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidTransition
		}
		shipment.Status, shipment.Version = next, shipment.Version+1
		if next == models.StatusDelivered {
			shipment.DeliveredAt = &now
		}
		return service.recordEvent(transaction, &shipment, next, "status_changed", statusTitle(next), "", nil, idempotencyKey, string(actorRole), actorID)
	})
	if err != nil {
		return nil, err
	}
	return &shipment, nil
}

func (service *ShipmentService) UpdateStops(ctx context.Context, shipmentID uint, stops int, actorRole models.Role, actorID, idempotencyKey string) error {
	if actorRole != models.RoleDeliveryEmployee && actorRole != models.RoleShippingAdmin || stops < 0 {
		return ErrForbidden
	}
	return service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		var shipment models.Shipment
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).First(&shipment, shipmentID).Error; err != nil {
			return ErrShipmentNotFound
		}
		if shipment.Status != models.StatusOutForDelivery {
			return ErrInvalidTransition
		}
		if err := transaction.Model(&shipment).Updates(map[string]any{"remaining_stops": stops, "version": shipment.Version + 1}).Error; err != nil {
			return err
		}
		return service.recordEvent(transaction, &shipment, shipment.Status, "stops_updated", fmt.Sprintf("%d stops remaining", stops), "", &stops, idempotencyKey, string(actorRole), actorID)
	})
}

func (service *ShipmentService) recordEvent(transaction *gorm.DB, shipment *models.Shipment, status models.ShipmentStatus, eventType, title, description string, stops *int, key, actorType, actorID string) error {
	event := models.ShipmentEvent{ShipmentID: shipment.ID, IdempotencyKey: key, EventType: eventType, Status: status, Title: title, Description: description, RemainingStops: stops, OccurredAt: time.Now().UTC(), CreatedByType: actorType, CreatedByID: actorID}
	if err := transaction.Create(&event).Error; err != nil {
		return err
	}
	if err := transaction.Create(&models.AuditLog{ShipmentID: shipment.ID, ActorType: actorType, ActorID: actorID, Action: eventType, NewStatus: status}).Error; err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"shipment_id": fmt.Sprint(shipment.ID), "tracking_number": shipment.TrackingNumber, "status": string(status)})
	if err != nil {
		return err
	}
	return transaction.Create(&models.OutboxEvent{ShipmentID: shipment.ID, EventType: eventType, Payload: string(payload), Status: "pending", NextAttemptAt: time.Now().UTC()}).Error
}

func validateCreateInput(input CreateShipmentInput, key string) error {
	if strings.TrimSpace(input.OrderID) == "" || strings.TrimSpace(input.CustomerID) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(input.Recipient.FirstName) == "" || strings.TrimSpace(input.Recipient.LastName) == "" || strings.TrimSpace(input.Recipient.Street) == "" || strings.TrimSpace(input.Recipient.HouseNumber) == "" || strings.TrimSpace(input.Recipient.PostalCode) == "" || strings.TrimSpace(input.Recipient.City) == "" || len(input.Recipient.CountryCode) != 2 || len(input.Items) == 0 {
		return ErrInvalidShipment
	}
	for _, item := range input.Items {
		if strings.TrimSpace(item.ProductID) == "" || strings.TrimSpace(item.Name) == "" || item.Quantity < 1 {
			return ErrInvalidShipment
		}
	}
	return nil
}

func warehouseAddress(warehouse config.Warehouse) models.AddressSnapshot {
	return models.AddressSnapshot{FirstName: warehouse.Name, Company: warehouse.Company, Street: warehouse.Street, HouseNumber: warehouse.HouseNumber, PostalCode: warehouse.PostalCode, City: warehouse.City, CountryCode: warehouse.CountryCode}
}

func canManageStatus(role models.Role, status models.ShipmentStatus) bool {
	if role == models.RoleShippingAdmin {
		return true
	}
	if role == models.RoleWarehouseEmployee {
		return status == models.StatusReceivedAtOrigin || status == models.StatusSorting || status == models.StatusInTransit || status == models.StatusArrivedDestinationHub
	}
	return role == models.RoleDeliveryEmployee && (status == models.StatusOutForDelivery || status == models.StatusDelivered || status == models.StatusDeliveryFailed)
}

func statusTitle(status models.ShipmentStatus) string {
	return strings.ReplaceAll(strings.ToUpper(string(status[:1]))+strings.ReplaceAll(string(status[1:]), "_", " "), "_", " ")
}

func generateNumber(prefix string) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	value := make([]byte, 6)
	for index := range value {
		random, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", fmt.Errorf("generate tracking number: %w", err)
		}
		value[index] = alphabet[random.Int64()]
	}
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().UTC().Format("20060102"), value), nil
}
