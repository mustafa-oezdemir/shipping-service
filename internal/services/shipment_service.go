package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/config"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidShipment     = errors.New("invalid shipment request")
	ErrShipmentNotFound    = errors.New("shipment not found")
	ErrReturnNotFound      = errors.New("return shipment not found")
	ErrInvalidTransition   = errors.New("invalid shipment status transition")
	ErrForbidden           = errors.New("operation is not permitted for this role")
	ErrInvalidETA          = errors.New("invalid estimated delivery range")
	ErrInvalidEvent        = errors.New("invalid shipment event")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for a different request")
)

var germanPostalCodePattern = regexp.MustCompile(`^\d{5}$`)

type CreateShipmentInput struct {
	OrderID        string
	CustomerID     string
	Recipient      models.AddressSnapshot
	Items          []ItemInput
	Carrier        string
	ServiceLevel   string
	EstimatedFrom  *time.Time
	EstimatedUntil *time.Time
}

type ItemInput struct {
	ProductID string
	Name      string
	SKU       string
	Quantity  int
}

type CreateReturnInput struct {
	OriginalShipmentID string
	OrderID            string
	CustomerID         string
	CustomerAddress    models.AddressSnapshot
	Items              []ItemInput
}

type ShipmentEventInput struct {
	EventType      string
	Title          string
	Description    string
	LocationName   string
	City           string
	PostalCode     string
	CountryCode    string
	OccurredAt     *time.Time
	RemainingStops *int
}

type CallbackPayload struct {
	EventID           string                 `json:"event_id"`
	ShipmentID        string                 `json:"shipment_id"`
	OrderID           string                 `json:"order_id"`
	TrackingNumber    string                 `json:"tracking_number"`
	ShipmentType      string                 `json:"shipment_type"`
	Status            string                 `json:"status"`
	StatusLabel       string                 `json:"status_label"`
	RemainingStops    *int                   `json:"remaining_stops,omitempty"`
	EstimatedDelivery *EstimatedDeliveryData `json:"estimated_delivery,omitempty"`
	OccurredAt        time.Time              `json:"occurred_at"`
}

type EstimatedDeliveryData struct {
	From  *time.Time `json:"from,omitempty"`
	Until *time.Time `json:"until,omitempty"`
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

func (service *ShipmentService) Create(ctx context.Context, input CreateShipmentInput, idempotencyKey, sourceService, requestID string) (*models.Shipment, bool, error) {
	if err := validateCreateInput(input, idempotencyKey); err != nil {
		return nil, false, err
	}
	if !validLength(sourceService, 40) {
		return nil, false, ErrInvalidShipment
	}
	var shipment models.Shipment
	created := true
	businessKey := outboundBusinessKey(input.OrderID)
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if existing, found, err := service.findShipmentByIdempotency(transaction, sourceService, idempotencyKey); err != nil {
			return err
		} else if found {
			if !shipmentMatchesCreate(existing, input) {
				return ErrIdempotencyConflict
			}
			shipment = existing
			created = false
			return nil
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
			PublicID:           generateIdentifier("shp"),
			ShipmentNumber:     shipmentNumber,
			TrackingNumber:     trackingNumber,
			ExternalOrderID:    strings.TrimSpace(input.OrderID),
			ExternalCustomerID: strings.TrimSpace(input.CustomerID),
			BusinessKey:        businessKey,
			SourceService:      sourceService,
			IdempotencyKey:     strings.TrimSpace(idempotencyKey),
			Status:             models.StatusCreated,
			Carrier:            strings.TrimSpace(input.Carrier),
			ServiceLevel:       strings.TrimSpace(input.ServiceLevel),
			Recipient:          normalizeAddress(input.Recipient),
			Sender:             warehouseAddress(service.warehouse),
			EstimatedFrom:      input.EstimatedFrom,
			EstimatedUntil:     input.EstimatedUntil,
		}
		if err := transaction.Create(&shipment).Error; err != nil {
			if isDuplicateError(err) {
				if existing, found, loadErr := service.findShipmentBySourceIdempotencyOrBusinessKey(transaction, sourceService, idempotencyKey, businessKey); loadErr != nil {
					return loadErr
				} else if found {
					if !shipmentMatchesCreate(existing, input) {
						return ErrIdempotencyConflict
					}
					shipment = existing
					created = false
					return nil
				}
			}
			return fmt.Errorf("create shipment: %w", err)
		}
		for _, item := range input.Items {
			if err := transaction.Create(&models.ShipmentItem{ShipmentID: shipment.ID, ExternalProductID: strings.TrimSpace(item.ProductID), Name: strings.TrimSpace(item.Name), SKU: strings.TrimSpace(item.SKU), Quantity: item.Quantity}).Error; err != nil {
				return fmt.Errorf("create shipment item: %w", err)
			}
		}
		_, err = service.recordEvent(transaction, &shipment, recordEventInput{
			SourceService:     sourceService,
			RequestID:         requestID,
			ActorType:         sourceService,
			ActorID:           strings.TrimSpace(input.CustomerID),
			Status:            models.StatusCreated,
			EventType:         "shipment_created",
			Title:             "Shipment created",
			IdempotencyKey:    idempotencyKey + "/created",
			OccurredAt:        time.Now().UTC(),
			CreateOutboxEvent: true,
		})
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return &shipment, created, nil
}

func (service *ShipmentService) CreateReturn(ctx context.Context, input CreateReturnInput, idempotencyKey, sourceService, requestID string) (*models.Shipment, bool, error) {
	if err := validateReturnInput(input, idempotencyKey); err != nil {
		return nil, false, err
	}
	if !validLength(sourceService, 40) {
		return nil, false, ErrInvalidShipment
	}
	var shipment models.Shipment
	created := true
	businessKey := returnBusinessKey(input.OrderID, input.OriginalShipmentID)
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if existing, found, err := service.findShipmentByIdempotency(transaction, sourceService, idempotencyKey); err != nil {
			return err
		} else if found {
			matches, matchErr := service.shipmentMatchesReturn(transaction, existing, input)
			if matchErr != nil {
				return matchErr
			}
			if !matches {
				return ErrIdempotencyConflict
			}
			shipment = existing
			created = false
			return nil
		}

		var original models.Shipment
		if err := transaction.Where("public_id = ?", strings.TrimSpace(input.OriginalShipmentID)).First(&original).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrShipmentNotFound
			}
			return err
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
			PublicID:           generateIdentifier("shp"),
			ShipmentNumber:     shipmentNumber,
			TrackingNumber:     trackingNumber,
			ExternalOrderID:    strings.TrimSpace(input.OrderID),
			ExternalCustomerID: strings.TrimSpace(input.CustomerID),
			BusinessKey:        businessKey,
			SourceService:      sourceService,
			IdempotencyKey:     strings.TrimSpace(idempotencyKey),
			OriginalShipmentID: &original.ID,
			IsReturn:           true,
			Status:             models.StatusReturnRequested,
			Carrier:            original.Carrier,
			ServiceLevel:       original.ServiceLevel,
			Sender:             normalizeAddress(input.CustomerAddress),
			Recipient:          warehouseAddress(service.warehouse),
		}
		if err := transaction.Create(&shipment).Error; err != nil {
			if isDuplicateError(err) {
				if existing, found, loadErr := service.findShipmentBySourceIdempotencyOrBusinessKey(transaction, sourceService, idempotencyKey, businessKey); loadErr != nil {
					return loadErr
				} else if found {
					matches, matchErr := service.shipmentMatchesReturn(transaction, existing, input)
					if matchErr != nil {
						return matchErr
					}
					if !matches {
						return ErrIdempotencyConflict
					}
					shipment = existing
					created = false
					return nil
				}
			}
			return fmt.Errorf("create return shipment: %w", err)
		}
		for _, item := range input.Items {
			if err := transaction.Create(&models.ShipmentItem{ShipmentID: shipment.ID, ExternalProductID: strings.TrimSpace(item.ProductID), Name: strings.TrimSpace(item.Name), SKU: strings.TrimSpace(item.SKU), Quantity: item.Quantity}).Error; err != nil {
				return fmt.Errorf("create return shipment item: %w", err)
			}
		}
		_, err = service.recordEvent(transaction, &shipment, recordEventInput{
			SourceService:     sourceService,
			RequestID:         requestID,
			ActorType:         sourceService,
			ActorID:           strings.TrimSpace(input.CustomerID),
			Status:            models.StatusReturnRequested,
			EventType:         "return_requested",
			Title:             "Return requested",
			IdempotencyKey:    idempotencyKey + "/created",
			OccurredAt:        time.Now().UTC(),
			CreateOutboxEvent: true,
		})
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return &shipment, created, nil
}

func (service *ShipmentService) GetPublic(ctx context.Context, trackingNumber string) (*models.Shipment, error) {
	var shipment models.Shipment
	if err := service.database.WithContext(ctx).Preload("Events", func(database *gorm.DB) *gorm.DB {
		return database.Order("occurred_at ASC")
	}).Where("tracking_number = ?", strings.TrimSpace(trackingNumber)).First(&shipment).Error; err != nil {
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

func (service *ShipmentService) GetByOrder(ctx context.Context, orderID string) (*models.Shipment, error) {
	var shipment models.Shipment
	if err := service.database.WithContext(ctx).Where("external_order_id = ? AND is_return = ?", strings.TrimSpace(orderID), false).Order("id ASC").First(&shipment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrShipmentNotFound
		}
		return nil, err
	}
	return &shipment, nil
}

func (service *ShipmentService) GetByTracking(ctx context.Context, trackingNumber string) (*models.Shipment, error) {
	return service.getByTracking(ctx, trackingNumber, false)
}

func (service *ShipmentService) GetReturnByTracking(ctx context.Context, trackingNumber string) (*models.Shipment, error) {
	return service.getByTracking(ctx, trackingNumber, true)
}

func (service *ShipmentService) ListEventsByTracking(ctx context.Context, trackingNumber string) ([]models.ShipmentEvent, *models.Shipment, error) {
	var shipment models.Shipment
	if err := service.database.WithContext(ctx).Preload("Events", func(database *gorm.DB) *gorm.DB {
		return database.Order("occurred_at ASC")
	}).Where("tracking_number = ?", strings.TrimSpace(trackingNumber)).First(&shipment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrShipmentNotFound
		}
		return nil, nil, err
	}
	return shipment.Events, &shipment, nil
}

func (service *ShipmentService) Transition(ctx context.Context, shipmentPublicID string, expected, next models.ShipmentStatus, actorRole models.Role, actorID, idempotencyKey, requestID, sourceService string) (*models.Shipment, bool, error) {
	if !canManageStatus(actorRole, next) {
		return nil, false, ErrForbidden
	}
	var shipment models.Shipment
	replayed := false
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if idempotencyKey != "" {
			var event models.ShipmentEvent
			if err := transaction.Where("source_service = ? AND idempotency_key = ?", sourceService, strings.TrimSpace(idempotencyKey)).First(&event).Error; err == nil {
				if err := transaction.First(&shipment, event.ShipmentID).Error; err != nil {
					return err
				}
				replayed = true
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id = ?", strings.TrimSpace(shipmentPublicID)).First(&shipment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrShipmentNotFound
			}
			return err
		}
		if shipment.Status != expected || !shipment.Status.CanTransitionTo(next) {
			return ErrInvalidTransition
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": next, "version": shipment.Version + 1}
		if next == models.StatusDelivered {
			updates["delivered_at"] = now
			stops := 0
			updates["remaining_stops"] = &stops
		}
		result := transaction.Model(&models.Shipment{}).Where("id = ? AND status = ? AND version = ?", shipment.ID, expected, shipment.Version).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidTransition
		}
		oldStatus := shipment.Status
		shipment.Status, shipment.Version = next, shipment.Version+1
		if next == models.StatusDelivered {
			shipment.DeliveredAt = &now
			stops := 0
			shipment.RemainingStops = &stops
		}
		_, err := service.recordEvent(transaction, &shipment, recordEventInput{SourceService: sourceService, RequestID: requestID, ActorType: string(actorRole), ActorID: strings.TrimSpace(actorID), OldStatus: oldStatus, Status: next, EventType: "status_changed", Title: statusTitle(next), IdempotencyKey: idempotencyKey, OccurredAt: now, CreateOutboxEvent: true})
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return &shipment, replayed, nil
}

func (service *ShipmentService) UpdateStops(ctx context.Context, shipmentPublicID string, stops int, actorRole models.Role, actorID, idempotencyKey, requestID, sourceService string) (*models.Shipment, bool, error) {
	if (actorRole != models.RoleDeliveryEmployee && actorRole != models.RoleShippingAdmin) || stops < 0 {
		return nil, false, ErrForbidden
	}
	var shipment models.Shipment
	replayed := false
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if idempotencyKey != "" {
			var event models.ShipmentEvent
			if err := transaction.Where("source_service = ? AND idempotency_key = ?", sourceService, strings.TrimSpace(idempotencyKey)).First(&event).Error; err == nil {
				if err := transaction.First(&shipment, event.ShipmentID).Error; err != nil {
					return err
				}
				replayed = true
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id = ?", strings.TrimSpace(shipmentPublicID)).First(&shipment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrShipmentNotFound
			}
			return err
		}
		if shipment.Status != models.StatusOutForDelivery {
			return ErrInvalidTransition
		}
		if err := transaction.Model(&shipment).Updates(map[string]any{"remaining_stops": stops, "version": shipment.Version + 1}).Error; err != nil {
			return err
		}
		shipment.Version++
		shipment.RemainingStops = &stops
		_, err := service.recordEvent(transaction, &shipment, recordEventInput{SourceService: sourceService, RequestID: requestID, ActorType: string(actorRole), ActorID: strings.TrimSpace(actorID), Status: shipment.Status, EventType: "stops_updated", Title: fmt.Sprintf("%d stops remaining", stops), RemainingStops: &stops, IdempotencyKey: idempotencyKey, OccurredAt: time.Now().UTC(), CreateOutboxEvent: true})
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return &shipment, replayed, nil
}

func (service *ShipmentService) UpdateETA(ctx context.Context, shipmentPublicID string, from, until *time.Time, actorRole models.Role, actorID, idempotencyKey, requestID, sourceService string) (*models.Shipment, bool, error) {
	if actorRole != models.RoleShippingAdmin && actorRole != models.RoleSupport && actorRole != models.RoleDeliveryEmployee {
		return nil, false, ErrForbidden
	}
	if from != nil && until != nil && until.Before(*from) {
		return nil, false, ErrInvalidETA
	}
	var shipment models.Shipment
	replayed := false
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if idempotencyKey != "" {
			var event models.ShipmentEvent
			if err := transaction.Where("source_service = ? AND idempotency_key = ?", sourceService, strings.TrimSpace(idempotencyKey)).First(&event).Error; err == nil {
				if err := transaction.First(&shipment, event.ShipmentID).Error; err != nil {
					return err
				}
				replayed = true
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id = ?", strings.TrimSpace(shipmentPublicID)).First(&shipment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrShipmentNotFound
			}
			return err
		}
		if err := transaction.Model(&shipment).Updates(map[string]any{"estimated_from": from, "estimated_until": until, "version": shipment.Version + 1}).Error; err != nil {
			return err
		}
		shipment.Version++
		shipment.EstimatedFrom = from
		shipment.EstimatedUntil = until
		_, err := service.recordEvent(transaction, &shipment, recordEventInput{SourceService: sourceService, RequestID: requestID, ActorType: string(actorRole), ActorID: strings.TrimSpace(actorID), Status: shipment.Status, EventType: "eta_updated", Title: "Estimated delivery updated", IdempotencyKey: idempotencyKey, OccurredAt: time.Now().UTC(), EstimatedFrom: from, EstimatedUntil: until, CreateOutboxEvent: true})
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return &shipment, replayed, nil
}

func (service *ShipmentService) AddEvent(ctx context.Context, shipmentPublicID string, input ShipmentEventInput, actorRole models.Role, actorID, idempotencyKey, requestID, sourceService string) (*models.ShipmentEvent, error) {
	if actorRole != models.RoleShippingAdmin && actorRole != models.RoleWarehouseEmployee && actorRole != models.RoleDeliveryEmployee && actorRole != models.RoleSupport {
		return nil, ErrForbidden
	}
	if err := validateEventInput(input); err != nil {
		return nil, err
	}
	var createdEvent *models.ShipmentEvent
	err := service.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if idempotencyKey != "" {
			var existing models.ShipmentEvent
			if err := transaction.Where("source_service = ? AND idempotency_key = ?", sourceService, strings.TrimSpace(idempotencyKey)).First(&existing).Error; err == nil {
				createdEvent = &existing
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var shipment models.Shipment
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id = ?", strings.TrimSpace(shipmentPublicID)).First(&shipment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrShipmentNotFound
			}
			return err
		}
		event, err := service.recordEvent(transaction, &shipment, recordEventInput{SourceService: sourceService, RequestID: requestID, ActorType: string(actorRole), ActorID: strings.TrimSpace(actorID), Status: shipment.Status, EventType: strings.TrimSpace(input.EventType), Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description), LocationName: strings.TrimSpace(input.LocationName), City: strings.TrimSpace(input.City), PostalCode: strings.TrimSpace(input.PostalCode), CountryCode: strings.ToUpper(strings.TrimSpace(input.CountryCode)), RemainingStops: input.RemainingStops, EstimatedFrom: shipment.EstimatedFrom, EstimatedUntil: shipment.EstimatedUntil, IdempotencyKey: idempotencyKey, OccurredAt: coalesceTime(input.OccurredAt, time.Now().UTC()), CreateOutboxEvent: true})
		if err != nil {
			return err
		}
		createdEvent = event
		return nil
	})
	if err != nil {
		return nil, err
	}
	return createdEvent, nil
}

type recordEventInput struct {
	SourceService     string
	RequestID         string
	ActorType         string
	ActorID           string
	OldStatus         models.ShipmentStatus
	Status            models.ShipmentStatus
	EventType         string
	Title             string
	Description       string
	LocationName      string
	City              string
	PostalCode        string
	CountryCode       string
	RemainingStops    *int
	EstimatedFrom     *time.Time
	EstimatedUntil    *time.Time
	IdempotencyKey    string
	OccurredAt        time.Time
	CreateOutboxEvent bool
}

func (service *ShipmentService) recordEvent(transaction *gorm.DB, shipment *models.Shipment, input recordEventInput) (*models.ShipmentEvent, error) {
	requestID := strings.TrimSpace(input.RequestID)
	if requestID == "" || len(requestID) > 80 {
		requestID = generateIdentifier("req")
	}
	if !validLength(input.SourceService, 80) || len(strings.TrimSpace(input.IdempotencyKey)) > 128 {
		return nil, ErrInvalidEvent
	}
	eventID := generateIdentifier("evt")
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = eventID
	}
	event := models.ShipmentEvent{EventID: eventID, ShipmentID: shipment.ID, SourceService: strings.TrimSpace(input.SourceService), IdempotencyKey: idempotencyKey, RequestID: requestID, EventType: strings.TrimSpace(input.EventType), Status: input.Status, Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description), LocationName: strings.TrimSpace(input.LocationName), City: strings.TrimSpace(input.City), PostalCode: strings.TrimSpace(input.PostalCode), CountryCode: strings.ToUpper(strings.TrimSpace(input.CountryCode)), RemainingStops: input.RemainingStops, EstimatedFrom: input.EstimatedFrom, EstimatedUntil: input.EstimatedUntil, OccurredAt: input.OccurredAt.UTC(), CreatedByType: strings.TrimSpace(input.ActorType), CreatedByID: strings.TrimSpace(input.ActorID)}
	if err := transaction.Create(&event).Error; err != nil {
		return nil, err
	}
	if err := transaction.Create(&models.AuditLog{ShipmentID: shipment.ID, ActorType: strings.TrimSpace(input.ActorType), ActorID: strings.TrimSpace(input.ActorID), Action: strings.TrimSpace(input.EventType), OldStatus: input.OldStatus, NewStatus: input.Status}).Error; err != nil {
		return nil, err
	}
	if input.CreateOutboxEvent {
		payload := CallbackPayload{EventID: event.EventID, ShipmentID: shipment.PublicID, OrderID: shipment.ExternalOrderID, TrackingNumber: shipment.TrackingNumber, ShipmentType: shipmentType(shipment.IsReturn), Status: string(input.Status), StatusLabel: statusTitle(input.Status), RemainingStops: input.RemainingStops, OccurredAt: event.OccurredAt}
		if payload.RemainingStops == nil {
			payload.RemainingStops = shipment.RemainingStops
		}
		if estimated := estimatedDeliveryPayload(input.EstimatedFrom, input.EstimatedUntil); estimated != nil {
			payload.EstimatedDelivery = estimated
		} else if estimated := estimatedDeliveryPayload(shipment.EstimatedFrom, shipment.EstimatedUntil); estimated != nil {
			payload.EstimatedDelivery = estimated
		}
		serialized, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		if err := transaction.Create(&models.OutboxEvent{EventID: event.EventID, ShipmentID: shipment.ID, ShipmentEventID: event.ID, RequestID: requestID, EventType: strings.TrimSpace(input.EventType), Payload: string(serialized), Status: models.OutboxStatusPending, NextAttemptAt: time.Now().UTC()}).Error; err != nil {
			return nil, err
		}
	}
	return &event, nil
}

func (service *ShipmentService) findShipmentByIdempotency(transaction *gorm.DB, sourceService, idempotencyKey string) (models.Shipment, bool, error) {
	var shipment models.Shipment
	if err := transaction.Preload("Items").Where("source_service = ? AND idempotency_key = ?", strings.TrimSpace(sourceService), strings.TrimSpace(idempotencyKey)).First(&shipment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Shipment{}, false, nil
		}
		return models.Shipment{}, false, err
	}
	return shipment, true, nil
}

func (service *ShipmentService) findShipmentBySourceIdempotencyOrBusinessKey(transaction *gorm.DB, sourceService, idempotencyKey, businessKey string) (models.Shipment, bool, error) {
	var shipment models.Shipment
	if err := transaction.Preload("Items").Where("(source_service = ? AND idempotency_key = ?) OR business_key = ?", strings.TrimSpace(sourceService), strings.TrimSpace(idempotencyKey), strings.TrimSpace(businessKey)).Order("id ASC").First(&shipment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Shipment{}, false, nil
		}
		return models.Shipment{}, false, err
	}
	return shipment, true, nil
}

func shipmentMatchesCreate(shipment models.Shipment, input CreateShipmentInput) bool {
	return !shipment.IsReturn &&
		shipment.ExternalOrderID == strings.TrimSpace(input.OrderID) &&
		shipment.ExternalCustomerID == strings.TrimSpace(input.CustomerID) &&
		shipment.Carrier == strings.TrimSpace(input.Carrier) &&
		shipment.ServiceLevel == strings.TrimSpace(input.ServiceLevel) &&
		shipment.Recipient == normalizeAddress(input.Recipient) &&
		itemsMatch(shipment.Items, input.Items)
}

func (service *ShipmentService) shipmentMatchesReturn(transaction *gorm.DB, shipment models.Shipment, input CreateReturnInput) (bool, error) {
	if !shipment.IsReturn || shipment.OriginalShipmentID == nil ||
		shipment.ExternalOrderID != strings.TrimSpace(input.OrderID) ||
		shipment.ExternalCustomerID != strings.TrimSpace(input.CustomerID) ||
		shipment.Sender != normalizeAddress(input.CustomerAddress) ||
		!itemsMatch(shipment.Items, input.Items) {
		return false, nil
	}
	var original models.Shipment
	if err := transaction.Select("public_id").First(&original, *shipment.OriginalShipmentID).Error; err != nil {
		return false, err
	}
	return original.PublicID == strings.TrimSpace(input.OriginalShipmentID), nil
}

func itemsMatch(stored []models.ShipmentItem, requested []ItemInput) bool {
	if len(stored) != len(requested) {
		return false
	}
	type itemKey struct{ productID, name, sku string }
	quantities := make(map[itemKey]int, len(stored))
	for _, item := range stored {
		key := itemKey{strings.TrimSpace(item.ExternalProductID), strings.TrimSpace(item.Name), strings.TrimSpace(item.SKU)}
		quantities[key] += item.Quantity
	}
	for _, item := range requested {
		key := itemKey{strings.TrimSpace(item.ProductID), strings.TrimSpace(item.Name), strings.TrimSpace(item.SKU)}
		quantities[key] -= item.Quantity
	}
	for _, quantity := range quantities {
		if quantity != 0 {
			return false
		}
	}
	return true
}

func (service *ShipmentService) getByTracking(ctx context.Context, trackingNumber string, isReturn bool) (*models.Shipment, error) {
	var shipment models.Shipment
	if err := service.database.WithContext(ctx).Where("tracking_number = ? AND is_return = ?", strings.TrimSpace(trackingNumber), isReturn).First(&shipment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if isReturn {
				return nil, ErrReturnNotFound
			}
			return nil, ErrShipmentNotFound
		}
		return nil, err
	}
	return &shipment, nil
}

func validateCreateInput(input CreateShipmentInput, key string) error {
	if !validLength(input.OrderID, 64) || !validLength(input.CustomerID, 64) || !validLength(key, 128) || len(input.Items) == 0 || len(input.Items) > 500 {
		return ErrInvalidShipment
	}
	if err := validateAddress(input.Recipient); err != nil {
		return err
	}
	if !validLength(input.Carrier, 80) || !validLength(input.ServiceLevel, 80) {
		return ErrInvalidShipment
	}
	if input.EstimatedFrom != nil && input.EstimatedUntil != nil && input.EstimatedUntil.Before(*input.EstimatedFrom) {
		return ErrInvalidETA
	}
	for _, item := range input.Items {
		if !validLength(item.ProductID, 64) || !validLength(item.Name, 255) || len(strings.TrimSpace(item.SKU)) > 100 || item.Quantity < 1 {
			return ErrInvalidShipment
		}
	}
	return nil
}

func validateReturnInput(input CreateReturnInput, key string) error {
	if !validLength(input.OriginalShipmentID, 64) || !validLength(input.OrderID, 64) || !validLength(input.CustomerID, 64) || !validLength(key, 128) || len(input.Items) == 0 || len(input.Items) > 500 {
		return ErrInvalidShipment
	}
	if err := validateAddress(input.CustomerAddress); err != nil {
		return err
	}
	for _, item := range input.Items {
		if !validLength(item.ProductID, 64) || !validLength(item.Name, 255) || len(strings.TrimSpace(item.SKU)) > 100 || item.Quantity < 1 {
			return ErrInvalidShipment
		}
	}
	return nil
}

func validateEventInput(input ShipmentEventInput) error {
	if strings.TrimSpace(input.EventType) == "" || strings.TrimSpace(input.Title) == "" {
		return ErrInvalidEvent
	}
	if strings.TrimSpace(input.CountryCode) != "" && len(strings.TrimSpace(input.CountryCode)) != 2 {
		return ErrInvalidEvent
	}
	if input.RemainingStops != nil && *input.RemainingStops < 0 {
		return ErrInvalidEvent
	}
	return nil
}

func validateAddress(address models.AddressSnapshot) error {
	if !validLength(address.FirstName, 100) || !validLength(address.LastName, 100) || !validLength(address.Street, 160) || !validLength(address.HouseNumber, 30) || !validLength(address.PostalCode, 20) || !validLength(address.City, 120) || len(strings.TrimSpace(address.Company)) > 160 || len(strings.TrimSpace(address.AddressLine2)) > 160 || len(strings.TrimSpace(address.State)) > 120 || len(strings.TrimSpace(address.Phone)) > 40 {
		return ErrInvalidShipment
	}
	countryCode := strings.ToUpper(strings.TrimSpace(address.CountryCode))
	if len(countryCode) != 2 {
		return ErrInvalidShipment
	}
	if countryCode == "DE" && !germanPostalCodePattern.MatchString(strings.TrimSpace(address.PostalCode)) {
		return ErrInvalidShipment
	}
	return nil
}

func validLength(value string, maximum int) bool {
	length := len(strings.TrimSpace(value))
	return length > 0 && length <= maximum
}

func normalizeAddress(address models.AddressSnapshot) models.AddressSnapshot {
	address.CountryCode = strings.ToUpper(strings.TrimSpace(address.CountryCode))
	address.FirstName = strings.TrimSpace(address.FirstName)
	address.LastName = strings.TrimSpace(address.LastName)
	address.Company = strings.TrimSpace(address.Company)
	address.Street = strings.TrimSpace(address.Street)
	address.HouseNumber = strings.TrimSpace(address.HouseNumber)
	address.AddressLine2 = strings.TrimSpace(address.AddressLine2)
	address.PostalCode = strings.TrimSpace(address.PostalCode)
	address.City = strings.TrimSpace(address.City)
	address.State = strings.TrimSpace(address.State)
	address.Phone = strings.TrimSpace(address.Phone)
	return address
}

func warehouseAddress(warehouse config.Warehouse) models.AddressSnapshot {
	return normalizeAddress(models.AddressSnapshot{FirstName: warehouse.Name, Company: warehouse.Company, Street: warehouse.Street, HouseNumber: warehouse.HouseNumber, PostalCode: warehouse.PostalCode, City: warehouse.City, CountryCode: warehouse.CountryCode})
}

func canManageStatus(role models.Role, status models.ShipmentStatus) bool {
	if role == models.RoleShippingAdmin {
		return true
	}
	if role == models.RoleWarehouseEmployee {
		return status == models.StatusReceivedAtOrigin || status == models.StatusSorting || status == models.StatusInTransit || status == models.StatusArrivedDestinationHub
	}
	if role == models.RoleSupport {
		return status == models.StatusDeliveryRescheduled || status == models.StatusReturnAuthorized || status == models.StatusReturnCompleted
	}
	return role == models.RoleDeliveryEmployee && (status == models.StatusOutForDelivery || status == models.StatusDelivered || status == models.StatusDeliveryFailed)
}

func statusTitle(status models.ShipmentStatus) string {
	if status == "" {
		return ""
	}
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

func generateIdentifier(prefix string) string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buffer)
}

func outboundBusinessKey(orderID string) string {
	return "shipment:" + strings.TrimSpace(orderID)
}

func returnBusinessKey(orderID, originalShipmentID string) string {
	return "return:" + strings.TrimSpace(orderID) + ":" + strings.TrimSpace(originalShipmentID)
}

func shipmentType(isReturn bool) string {
	if isReturn {
		return "return"
	}
	return "outbound"
}

func estimatedDeliveryPayload(from, until *time.Time) *EstimatedDeliveryData {
	if from == nil && until == nil {
		return nil
	}
	return &EstimatedDeliveryData{From: from, Until: until}
}

func coalesceTime(value *time.Time, fallback time.Time) time.Time {
	if value == nil {
		return fallback
	}
	return value.UTC()
}

func isDuplicateError(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
