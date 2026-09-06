package models

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

type ShipmentStatus string

type OutboxStatus string

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

const (
	OutboxStatusPending    OutboxStatus = "pending"
	OutboxStatusProcessing OutboxStatus = "processing"
	OutboxStatusDelivered  OutboxStatus = "delivered"
	OutboxStatusFailed     OutboxStatus = "failed"
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
	AddressLine2 string `json:"address_line_2"`
	PostalCode   string `json:"postal_code"`
	City         string `json:"city"`
	State        string `json:"state"`
	CountryCode  string `json:"country_code"`
	Phone        string `json:"phone"`
}

type Shipment struct {
	gorm.Model
	PublicID           string          `gorm:"size:64;uniqueIndex;not null"`
	ShipmentNumber     string          `gorm:"size:64;uniqueIndex;not null"`
	HandoverCode       *string         `gorm:"size:64;uniqueIndex"`
	TrackingNumber     string          `gorm:"size:64;uniqueIndex;not null"`
	ExternalOrderID    string          `gorm:"size:64;index;not null"`
	ExternalCustomerID string          `gorm:"size:64;index;not null"`
	BusinessKey        string          `gorm:"size:191;uniqueIndex;not null"`
	SourceService      string          `gorm:"size:80;not null;uniqueIndex:idx_shipments_source_idempotency,priority:1"`
	IdempotencyKey     string          `gorm:"size:128;not null;uniqueIndex:idx_shipments_source_idempotency,priority:2"`
	OriginalShipmentID *uint           `gorm:"index"`
	IsReturn           bool            `gorm:"not null;default:false;index"`
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
	EventID        string         `gorm:"size:64;uniqueIndex;not null"`
	ShipmentID     uint           `gorm:"not null;index"`
	SourceService  string         `gorm:"size:80;not null;uniqueIndex:idx_shipment_events_source_idempotency,priority:1"`
	IdempotencyKey string         `gorm:"size:128;not null;uniqueIndex:idx_shipment_events_source_idempotency,priority:2"`
	RequestID      string         `gorm:"size:80;index;not null"`
	EventType      string         `gorm:"size:64;not null"`
	Status         ShipmentStatus `gorm:"size:50;index;not null"`
	Title          string         `gorm:"size:255;not null"`
	Description    string         `gorm:"type:text"`
	LocationName   string         `gorm:"size:255"`
	City           string         `gorm:"size:120"`
	PostalCode     string         `gorm:"size:20"`
	CountryCode    string         `gorm:"size:2"`
	RemainingStops *int
	EstimatedFrom  *time.Time
	EstimatedUntil *time.Time
	OccurredAt     time.Time `gorm:"not null;index"`
	CreatedByType  string    `gorm:"size:40;not null"`
	CreatedByID    string    `gorm:"size:64"`
}

type AuditLog struct {
	gorm.Model
	ShipmentID  uint           `gorm:"not null;default:0;index"`
	ActorUserID *uint          `gorm:"index"`
	ActorType   string         `gorm:"size:40;not null"`
	ActorID     string         `gorm:"size:64"`
	Action      string         `gorm:"size:80;not null"`
	EntityType  string         `gorm:"size:40;index"`
	EntityID    string         `gorm:"size:64;index"`
	OldValue    string         `gorm:"type:text"`
	NewValue    string         `gorm:"type:text"`
	RequestID   string         `gorm:"size:80;index"`
	OldStatus   ShipmentStatus `gorm:"size:50"`
	NewStatus   ShipmentStatus `gorm:"size:50"`
}

type OutboxEvent struct {
	gorm.Model
	EventID         string       `gorm:"size:64;uniqueIndex;not null"`
	ShipmentID      uint         `gorm:"not null;index"`
	ShipmentEventID uint         `gorm:"not null;uniqueIndex"`
	RequestID       string       `gorm:"size:80;index;not null"`
	EventType       string       `gorm:"size:80;not null"`
	Payload         string       `gorm:"type:json;not null"`
	Status          OutboxStatus `gorm:"size:20;index;not null;default:pending"`
	Attempts        int          `gorm:"not null;default:0"`
	LastError       string       `gorm:"size:255"`
	NextAttemptAt   time.Time    `gorm:"index;not null"`
	DeliveredAt     *time.Time
}

type Role string

const (
	RoleAdmin             Role = "admin"
	RoleEmployee          Role = "employee"
	RoleShippingAdmin     Role = "shipping_admin"
	RoleWarehouseEmployee Role = "warehouse_employee"
	RoleDeliveryEmployee  Role = "delivery_employee"
	RoleSupport           Role = "support"
)

func (role Role) IsPersonnelRole() bool { return role == RoleAdmin || role == RoleEmployee }

type User struct {
	gorm.Model
	FirstName            string     `gorm:"size:100;not null"`
	LastName             string     `gorm:"size:100;not null"`
	Email                string     `gorm:"size:254;uniqueIndex;not null"`
	PasswordHash         string     `gorm:"size:255;not null"`
	Role                 Role       `gorm:"size:20;index;not null"`
	ProfileImageFilename string     `gorm:"size:64;not null;default:''"`
	IsActive             bool       `gorm:"not null;default:true;index"`
	SecurityVersion      uint64     `gorm:"not null;default:1"`
	LastLoginAt          *time.Time `gorm:"index"`
}

func (user User) FullName() string { return user.FirstName + " " + user.LastName }

func (user User) Initials() string {
	initials := ""
	for _, value := range []string{user.FirstName, user.LastName} {
		characters := []rune(strings.TrimSpace(value))
		if len(characters) > 0 {
			initials += strings.ToUpper(string(characters[0]))
		}
	}
	return initials
}

type BrowserSession struct {
	TokenHash       string    `gorm:"size:64;primaryKey"`
	UserID          uint      `gorm:"not null;index"`
	SecurityVersion uint64    `gorm:"not null"`
	ExpiresAt       time.Time `gorm:"not null;index"`
	CreatedAt       time.Time
}
