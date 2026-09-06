package handlers

import (
	"strings"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
)

type createShipmentRequest struct {
	OrderID        string         `json:"order_id" binding:"required,max=64"`
	CustomerID     string         `json:"customer_id" binding:"required,max=64"`
	HandoverCode   string         `json:"handover_code" binding:"required,max=64"`
	Recipient      addressRequest `json:"recipient" binding:"required"`
	Items          []itemRequest  `json:"items" binding:"required,min=1"`
	Carrier        string         `json:"carrier" binding:"required,max=80"`
	ServiceLevel   string         `json:"service_level" binding:"required,max=80"`
	EstimatedFrom  *time.Time     `json:"estimated_from,omitempty"`
	EstimatedUntil *time.Time     `json:"estimated_until,omitempty"`
}

type createReturnRequest struct {
	OriginalShipmentID string         `json:"original_shipment_id" binding:"required,max=64"`
	OrderID            string         `json:"order_id" binding:"required,max=64"`
	CustomerID         string         `json:"customer_id" binding:"required,max=64"`
	Sender             addressRequest `json:"sender" binding:"required"`
	Items              []itemRequest  `json:"items" binding:"required,min=1"`
}

type addressRequest struct {
	FirstName    string `json:"first_name" binding:"required,max=100"`
	LastName     string `json:"last_name" binding:"required,max=100"`
	Company      string `json:"company,omitempty" binding:"max=160"`
	Street       string `json:"street" binding:"required,max=160"`
	HouseNumber  string `json:"house_number" binding:"required,max=30"`
	AddressLine2 string `json:"address_line_2,omitempty" binding:"max=160"`
	PostalCode   string `json:"postal_code" binding:"required,max=20"`
	City         string `json:"city" binding:"required,max=120"`
	State        string `json:"state,omitempty" binding:"max=120"`
	CountryCode  string `json:"country_code" binding:"required,len=2"`
	Phone        string `json:"phone,omitempty" binding:"max=40"`
}

type itemRequest struct {
	ProductID string `json:"product_id" binding:"required,max=64"`
	Name      string `json:"name" binding:"required,max=255"`
	SKU       string `json:"sku,omitempty" binding:"max=100"`
	Quantity  int    `json:"quantity" binding:"required,gt=0"`
}

type transitionShipmentRequest struct {
	ExpectedStatus models.ShipmentStatus `json:"expected_status" binding:"required"`
	Status         models.ShipmentStatus `json:"status" binding:"required"`
}

type updateStopsRequest struct {
	RemainingStops int `json:"remaining_stops" binding:"gte=0,lte=500"`
}

type updateETARequest struct {
	EstimatedDelivery estimatedDeliveryRequest `json:"estimated_delivery" binding:"required"`
}

type estimatedDeliveryRequest struct {
	From  *time.Time `json:"from,omitempty"`
	Until *time.Time `json:"until,omitempty"`
}

type createEventRequest struct {
	EventType      string     `json:"event_type" binding:"required,max=64"`
	Title          string     `json:"title" binding:"required,max=255"`
	Description    string     `json:"description,omitempty" binding:"max=4000"`
	LocationName   string     `json:"location_name,omitempty" binding:"max=255"`
	City           string     `json:"city,omitempty" binding:"max=120"`
	PostalCode     string     `json:"postal_code,omitempty" binding:"max=20"`
	CountryCode    string     `json:"country_code,omitempty" binding:"omitempty,len=2"`
	OccurredAt     *time.Time `json:"occurred_at,omitempty"`
	RemainingStops *int       `json:"remaining_stops,omitempty"`
}

type createdShipmentResponse struct {
	ShipmentID        string                     `json:"shipment_id"`
	HandoverCode      string                     `json:"handover_code"`
	TrackingNumber    string                     `json:"tracking_number"`
	ShipmentType      string                     `json:"shipment_type"`
	Status            string                     `json:"status"`
	StatusLabel       string                     `json:"status_label"`
	EstimatedDelivery *estimatedDeliveryResponse `json:"estimated_delivery,omitempty"`
	IdempotentReplay  bool                       `json:"idempotent_replay"`
}

type shipmentResponse struct {
	ShipmentID        string                     `json:"shipment_id"`
	OrderID           string                     `json:"order_id"`
	HandoverCode      string                     `json:"handover_code"`
	TrackingNumber    string                     `json:"tracking_number"`
	ShipmentType      string                     `json:"shipment_type"`
	Status            string                     `json:"status"`
	StatusLabel       string                     `json:"status_label"`
	RemainingStops    *int                       `json:"remaining_stops,omitempty"`
	DeliveredAt       *time.Time                 `json:"delivered_at,omitempty"`
	EstimatedDelivery *estimatedDeliveryResponse `json:"estimated_delivery,omitempty"`
}

type shipmentEventResponse struct {
	EventID           string                     `json:"event_id"`
	Status            string                     `json:"status"`
	Title             string                     `json:"title"`
	Description       string                     `json:"description,omitempty"`
	Location          string                     `json:"location,omitempty"`
	RemainingStops    *int                       `json:"remaining_stops,omitempty"`
	EstimatedDelivery *estimatedDeliveryResponse `json:"estimated_delivery,omitempty"`
	OccurredAt        time.Time                  `json:"occurred_at"`
}

type estimatedDeliveryResponse struct {
	From  *time.Time `json:"from,omitempty"`
	Until *time.Time `json:"until,omitempty"`
}

func (request createShipmentRequest) toServiceInput() services.CreateShipmentInput {
	items := make([]services.ItemInput, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, services.ItemInput{ProductID: item.ProductID, Name: item.Name, SKU: item.SKU, Quantity: item.Quantity})
	}
	return services.CreateShipmentInput{OrderID: request.OrderID, CustomerID: request.CustomerID, HandoverCode: request.HandoverCode, Recipient: request.Recipient.toModel(), Items: items, Carrier: request.Carrier, ServiceLevel: request.ServiceLevel, EstimatedFrom: request.EstimatedFrom, EstimatedUntil: request.EstimatedUntil}
}

func (request createReturnRequest) toServiceInput() services.CreateReturnInput {
	items := make([]services.ItemInput, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, services.ItemInput{ProductID: item.ProductID, Name: item.Name, SKU: item.SKU, Quantity: item.Quantity})
	}
	return services.CreateReturnInput{OriginalShipmentID: request.OriginalShipmentID, OrderID: request.OrderID, CustomerID: request.CustomerID, CustomerAddress: request.Sender.toModel(), Items: items}
}

func (request addressRequest) toModel() models.AddressSnapshot {
	return models.AddressSnapshot{FirstName: request.FirstName, LastName: request.LastName, Company: request.Company, Street: request.Street, HouseNumber: request.HouseNumber, AddressLine2: request.AddressLine2, PostalCode: request.PostalCode, City: request.City, State: request.State, CountryCode: request.CountryCode, Phone: request.Phone}
}

func (request createEventRequest) toServiceInput() services.ShipmentEventInput {
	return services.ShipmentEventInput{EventType: request.EventType, Title: request.Title, Description: request.Description, LocationName: request.LocationName, City: request.City, PostalCode: request.PostalCode, CountryCode: request.CountryCode, OccurredAt: request.OccurredAt, RemainingStops: request.RemainingStops}
}

func newCreatedShipmentResponse(shipment *models.Shipment, replay bool) createdShipmentResponse {
	return createdShipmentResponse{ShipmentID: shipment.PublicID, HandoverCode: shipmentHandoverCode(shipment), TrackingNumber: shipment.TrackingNumber, ShipmentType: shipmentTypeLabel(shipment.IsReturn), Status: string(shipment.Status), StatusLabel: statusLabel(shipment.Status), EstimatedDelivery: newEstimatedDeliveryResponse(shipment.EstimatedFrom, shipment.EstimatedUntil), IdempotentReplay: replay}
}

func newShipmentResponse(shipment *models.Shipment) shipmentResponse {
	return shipmentResponse{ShipmentID: shipment.PublicID, OrderID: shipment.ExternalOrderID, HandoverCode: shipmentHandoverCode(shipment), TrackingNumber: shipment.TrackingNumber, ShipmentType: shipmentTypeLabel(shipment.IsReturn), Status: string(shipment.Status), StatusLabel: statusLabel(shipment.Status), RemainingStops: shipment.RemainingStops, DeliveredAt: shipment.DeliveredAt, EstimatedDelivery: newEstimatedDeliveryResponse(shipment.EstimatedFrom, shipment.EstimatedUntil)}
}

func shipmentHandoverCode(shipment *models.Shipment) string {
	if shipment.HandoverCode == nil {
		return ""
	}
	return *shipment.HandoverCode
}

func newShipmentEventResponse(event *models.ShipmentEvent) shipmentEventResponse {
	return shipmentEventResponse{EventID: event.EventID, Status: string(event.Status), Title: event.Title, Description: event.Description, Location: eventLocation(event), RemainingStops: event.RemainingStops, EstimatedDelivery: newEstimatedDeliveryResponse(event.EstimatedFrom, event.EstimatedUntil), OccurredAt: event.OccurredAt}
}

func newEstimatedDeliveryResponse(from, until *time.Time) *estimatedDeliveryResponse {
	if from == nil && until == nil {
		return nil
	}
	return &estimatedDeliveryResponse{From: from, Until: until}
}

func eventLocation(event *models.ShipmentEvent) string {
	switch {
	case event.LocationName != "" && event.City != "":
		return event.LocationName + ", " + event.City
	case event.LocationName != "":
		return event.LocationName
	default:
		return event.City
	}
}

func shipmentTypeLabel(isReturn bool) string {
	if isReturn {
		return "return"
	}
	return "outbound"
}

func statusLabel(status models.ShipmentStatus) string {
	if status == "" {
		return ""
	}
	label := strings.ReplaceAll(string(status), "_", " ")
	return strings.ToUpper(label[:1]) + label[1:]
}
