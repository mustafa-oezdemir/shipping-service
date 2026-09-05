package models

import (
	"time"

	"gorm.io/gorm"
)

type ShipmentStatus string

const (
	StatusCreated               ShipmentStatus = "created"
	StatusLabelCreated          ShipmentStatus = "label_created"
	StatusReadyForPickup        ShipmentStatus = "ready_for_pickup"
	StatusHandedOver            ShipmentStatus = "handed_over"
	StatusReceivedAtOrigin      ShipmentStatus = "received_at_origin"
	StatusSorting               ShipmentStatus = "sorting"
	StatusInTransit             ShipmentStatus = "in_transit"
	StatusArrivedDestinationHub ShipmentStatus = "arrived_at_destination_hub"
	StatusOutForDelivery        ShipmentStatus = "out_for_delivery"
	StatusDelivered             ShipmentStatus = "delivered"
	StatusDeliveryFailed        ShipmentStatus = "delivery_failed"
	StatusDeliveryRescheduled   ShipmentStatus = "delivery_rescheduled"
	StatusReturnRequested       ShipmentStatus = "return_requested"
	StatusReturnAuthorized      ShipmentStatus = "return_authorized"
	StatusReturnLabelCreated    ShipmentStatus = "return_label_created"
	StatusReturnInTransit       ShipmentStatus = "return_in_transit"
	StatusReturnReceived        ShipmentStatus = "return_received"
	StatusReturnCompleted       ShipmentStatus = "return_completed"
	StatusCancelled             ShipmentStatus = "cancelled"
)

func (status ShipmentStatus) CanTransitionTo(next ShipmentStatus) bool {
	transitions := map[ShipmentStatus]map[ShipmentStatus]bool{
		StatusCreated:               {StatusLabelCreated: true, StatusCancelled: true},
		StatusLabelCreated:          {StatusReadyForPickup: true, StatusCancelled: true},
		StatusReadyForPickup:        {StatusHandedOver: true, StatusCancelled: true},
		StatusHandedOver:            {StatusReceivedAtOrigin: true},
		StatusReceivedAtOrigin:      {StatusSorting: true},
		StatusSorting:               {StatusInTransit: true},
		StatusInTransit:             {StatusArrivedDestinationHub: true},
		StatusArrivedDestinationHub: {StatusOutForDelivery: true},
		StatusOutForDelivery:        {StatusDelivered: true, StatusDeliveryFailed: true},
		StatusDeliveryFailed:        {StatusDeliveryRescheduled: true, StatusReturnRequested: true},
		StatusDeliveryRescheduled:   {StatusOutForDelivery: true, StatusReturnRequested: true},
		StatusReturnRequested:       {StatusReturnAuthorized: true},
		StatusReturnAuthorized:      {StatusReturnLabelCreated: true},
		StatusReturnLabelCreated:    {StatusReturnInTransit: true},
		StatusReturnInTransit:       {StatusReturnReceived: true},
		StatusReturnReceived:        {StatusReturnCompleted: true},
	}
	return transitions[status][next]
}

type AddressSnapshot struct {
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Company      string `json:"company"`
	Street       string `json:"street"`
	HouseNumber  string `json:"house_number"`
	AddressLine2 string `json:"address_line2"`
	PostalCode   string `json:"postal_code"`
	City         string `json:"city"`
	State        string `json:"state"`
	CountryCode  string `json:"country_code"`
	Phone        string `json:"phone"`
}

type Shipment struct {
	gorm.Model
	ShipmentNumber     string          `gorm:"size:64;uniqueIndex;not null"`
	TrackingNumber     string          `gorm:"size:64;uniqueIndex;not null"`
	ExternalOrderID    string          `gorm:"size:64;uniqueIndex:idx_shipments_order_direction;not null"`
	ExternalCustomerID string          `gorm:"size:64;index;not null"`
	IdempotencyKey     string          `gorm:"size:128;uniqueIndex;not null"`
	OriginalShipmentID *uint           `gorm:"index"`
	IsReturn           bool            `gorm:"uniqueIndex:idx_shipments_order_direction;not null;default:false"`
	Status             ShipmentStatus  `gorm:"size:50;index;not null"`
	Carrier            string          `gorm:"size:80;not null"`
	ServiceLevel       string          `gorm:"size:80;not null"`
	Sender             AddressSnapshot `gorm:"embedded;embeddedPrefix:sender_"`
	Recipient          AddressSnapshot `gorm:"embedded;embeddedPrefix:recipient_"`
	EstimatedFrom      *time.Time
	EstimatedUntil     *time.Time
	DeliveredAt        *time.Time
	CurrentStop        string `gorm:"size:255"`
	RemainingStops     *int
	Version            uint `gorm:"not null;default:1"`
	Events             []ShipmentEvent
	Items              []ShipmentItem
}

type ShipmentItem struct {
	gorm.Model
	ShipmentID        uint   `gorm:"not null;index"`
	ExternalProductID string `gorm:"size:64;not null"`
	Name              string `gorm:"size:255;not null"`
	SKU               string `gorm:"size:100"`
	Quantity          int    `gorm:"not null"`
}

type ShipmentEvent struct {
	gorm.Model
	ShipmentID     uint           `gorm:"not null;index"`
	IdempotencyKey string         `gorm:"size:128;uniqueIndex"`
	EventType      string         `gorm:"size:64;not null"`
	Status         ShipmentStatus `gorm:"size:50;index;not null"`
	Title          string         `gorm:"size:255;not null"`
	Description    string         `gorm:"type:text"`
	LocationName   string         `gorm:"size:255"`
	City           string         `gorm:"size:120"`
	PostalCode     string         `gorm:"size:20"`
	CountryCode    string         `gorm:"size:2"`
	RemainingStops *int
	OccurredAt     time.Time `gorm:"not null;index"`
	CreatedByType  string    `gorm:"size:40;not null"`
	CreatedByID    string    `gorm:"size:64"`
}

type AuditLog struct {
	gorm.Model
	ShipmentID uint           `gorm:"not null;index"`
	ActorType  string         `gorm:"size:40;not null"`
	ActorID    string         `gorm:"size:64"`
	Action     string         `gorm:"size:80;not null"`
	OldStatus  ShipmentStatus `gorm:"size:50"`
	NewStatus  ShipmentStatus `gorm:"size:50"`
}

type OutboxEvent struct {
	gorm.Model
	ShipmentID    uint      `gorm:"not null;index"`
	EventType     string    `gorm:"size:80;not null"`
	Payload       string    `gorm:"type:json;not null"`
	Status        string    `gorm:"size:20;index;not null;default:pending"`
	Attempts      int       `gorm:"not null;default:0"`
	NextAttemptAt time.Time `gorm:"index;not null"`
}

type Role string

const (
	RoleShippingAdmin     Role = "shipping_admin"
	RoleWarehouseEmployee Role = "warehouse_employee"
	RoleDeliveryEmployee  Role = "delivery_employee"
	RoleSupport           Role = "support"
)
