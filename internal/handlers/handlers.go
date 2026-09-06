package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/mustafa-oezdemir/shipping-service/internal/httpapi"
	"github.com/mustafa-oezdemir/shipping-service/internal/middleware"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"github.com/skip2/go-qrcode"
	"gorm.io/gorm"
)

const maxInternalJSONBytes int64 = 1 << 20

type Handler struct {
	service       *services.ShipmentService
	database      *gorm.DB
	publicBaseURL string
}

func New(service *services.ShipmentService, database *gorm.DB, publicBaseURL string) *Handler {
	return &Handler{service: service, database: database, publicBaseURL: strings.TrimRight(publicBaseURL, "/")}
}

func (handler *Handler) Index(context *gin.Context) {
	context.JSON(http.StatusOK, gin.H{"service": "shipping-service", "status": "ok"})
}

func (handler *Handler) Health(context *gin.Context) {
	context.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (handler *Handler) Ready(context *gin.Context) {
	sqlDB, err := handler.database.DB()
	if err != nil || sqlDB.PingContext(context.Request.Context()) != nil {
		context.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	context.JSON(http.StatusOK, gin.H{"status": "ready"})
}

func (handler *Handler) CreateShipment(context *gin.Context) {
	var request createShipmentRequest
	if err := bindInternalJSON(context, &request); err != nil {
		respondInvalidJSON(context, err, "Invalid shipment payload")
		return
	}
	shipment, created, err := handler.service.Create(context.Request.Context(), request.toServiceInput(), context.GetHeader("Idempotency-Key"), middleware.SourceService(context, "ecommerce-gin"), middleware.CurrentRequestID(context))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	httpapi.Success(context, status, newCreatedShipmentResponse(shipment, !created))
}

func (handler *Handler) CreateReturn(context *gin.Context) {
	var request createReturnRequest
	if err := bindInternalJSON(context, &request); err != nil {
		respondInvalidJSON(context, err, "Invalid return payload")
		return
	}
	shipment, created, err := handler.service.CreateReturn(context.Request.Context(), request.toServiceInput(), context.GetHeader("Idempotency-Key"), middleware.SourceService(context, "ecommerce-gin"), middleware.CurrentRequestID(context))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	httpapi.Success(context, status, newCreatedShipmentResponse(shipment, !created))
}

func (handler *Handler) GetShipmentForOrder(context *gin.Context) {
	shipment, err := handler.service.GetByOrder(context.Request.Context(), context.Param("orderID"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	httpapi.Success(context, http.StatusOK, newShipmentResponse(shipment))
}

func (handler *Handler) GetShipmentByTracking(context *gin.Context) {
	shipment, err := handler.service.GetPublic(context.Request.Context(), context.Param("trackingNumber"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	httpapi.Success(context, http.StatusOK, newShipmentResponse(shipment))
}

func (handler *Handler) GetReturnByTracking(context *gin.Context) {
	shipment, err := handler.service.GetReturnByTracking(context.Request.Context(), context.Param("trackingNumber"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	httpapi.Success(context, http.StatusOK, newShipmentResponse(shipment))
}

func (handler *Handler) ShipmentEvents(context *gin.Context) {
	events, _, err := handler.service.ListEventsByTracking(context.Request.Context(), context.Param("trackingNumber"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	response := make([]shipmentEventResponse, 0, len(events))
	for index := range events {
		response = append(response, newShipmentEventResponse(&events[index]))
	}
	httpapi.Success(context, http.StatusOK, response)
}

func (handler *Handler) TransitionShipment(context *gin.Context) {
	var request transitionShipmentRequest
	if err := bindInternalJSON(context, &request); err != nil {
		respondInvalidJSON(context, err, "expected_status and status are required")
		return
	}
	shipment, _, err := handler.service.Transition(context.Request.Context(), context.Param("id"), request.ExpectedStatus, request.Status, middleware.CurrentRole(context), context.GetHeader("X-Actor-ID"), context.GetHeader("Idempotency-Key"), middleware.CurrentRequestID(context), middleware.SourceService(context, "shipping-service"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	httpapi.Success(context, http.StatusOK, newShipmentResponse(shipment))
}

func (handler *Handler) UpdateStops(context *gin.Context) {
	var request updateStopsRequest
	if err := bindInternalJSON(context, &request); err != nil {
		respondInvalidJSON(context, err, "remaining_stops must be between 0 and 500")
		return
	}
	shipment, _, err := handler.service.UpdateStops(context.Request.Context(), context.Param("id"), request.RemainingStops, middleware.CurrentRole(context), context.GetHeader("X-Actor-ID"), context.GetHeader("Idempotency-Key"), middleware.CurrentRequestID(context), middleware.SourceService(context, "shipping-service"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	httpapi.Success(context, http.StatusOK, newShipmentResponse(shipment))
}

func (handler *Handler) UpdateETA(context *gin.Context) {
	var request updateETARequest
	if err := bindInternalJSON(context, &request); err != nil {
		respondInvalidJSON(context, err, "estimated_delivery.from/until payload is required")
		return
	}
	shipment, _, err := handler.service.UpdateETA(context.Request.Context(), context.Param("id"), request.EstimatedDelivery.From, request.EstimatedDelivery.Until, middleware.CurrentRole(context), context.GetHeader("X-Actor-ID"), context.GetHeader("Idempotency-Key"), middleware.CurrentRequestID(context), middleware.SourceService(context, "shipping-service"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	httpapi.Success(context, http.StatusOK, newShipmentResponse(shipment))
}

func (handler *Handler) CreateShipmentEvent(context *gin.Context) {
	var request createEventRequest
	if err := bindInternalJSON(context, &request); err != nil {
		respondInvalidJSON(context, err, "event_type and title are required")
		return
	}
	event, err := handler.service.AddEvent(context.Request.Context(), context.Param("id"), request.toServiceInput(), middleware.CurrentRole(context), context.GetHeader("X-Actor-ID"), context.GetHeader("Idempotency-Key"), middleware.CurrentRequestID(context), middleware.SourceService(context, "shipping-service"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	httpapi.Success(context, http.StatusCreated, newShipmentEventResponse(event))
}

func bindInternalJSON(context *gin.Context, target any) error {
	context.Request.Body = http.MaxBytesReader(context.Writer, context.Request.Body, maxInternalJSONBytes)
	decoder := json.NewDecoder(context.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

func respondInvalidJSON(context *gin.Context, err error, message string) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		httpapi.Error(context, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "Request body exceeds 1 MiB", middleware.CurrentRequestID(context))
		return
	}
	httpapi.Error(context, http.StatusBadRequest, "INVALID_REQUEST", message, middleware.CurrentRequestID(context))
}

func (handler *Handler) PublicTracking(context *gin.Context) {
	shipment, err := handler.service.GetPublic(context.Request.Context(), context.Param("trackingNumber"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	context.HTML(http.StatusOK, "tracking.tmpl", gin.H{"Shipment": shipment})
}

func (handler *Handler) InternalShipment(context *gin.Context) {
	id, err := strconv.ParseUint(context.Param("id"), 10, 64)
	if err != nil || id == 0 {
		context.AbortWithStatus(http.StatusNotFound)
		return
	}
	shipment, err := handler.service.GetInternal(context.Request.Context(), uint(id))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	context.HTML(http.StatusOK, "shipment.tmpl", gin.H{"Shipment": shipment})
}

func (handler *Handler) TrackingQR(context *gin.Context) {
	shipment, err := handler.service.GetByTracking(context.Request.Context(), context.Param("trackingNumber"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	scanURL := handler.publicBaseURL + "/scan/shipment/" + url.PathEscape(shipment.PublicID)
	png, err := qrcode.Encode(scanURL, qrcode.Medium, 256)
	if err != nil {
		context.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	context.Data(http.StatusOK, "image/png", png)
}

func (handler *Handler) Label(context *gin.Context) {
	id, err := strconv.ParseUint(context.Param("id"), 10, 64)
	if err != nil || id == 0 {
		context.AbortWithStatus(http.StatusNotFound)
		return
	}
	shipment, err := handler.service.GetInternal(context.Request.Context(), uint(id))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	context.HTML(http.StatusOK, "label.tmpl", gin.H{"Shipment": shipment, "TrackingURL": handler.trackingURL(shipment.TrackingNumber)})
}

func (handler *Handler) trackingURL(trackingNumber string) string {
	return handler.publicBaseURL + "/track/" + url.PathEscape(trackingNumber)
}

func (handler *Handler) Dashboard(context *gin.Context) {
	var shipments []models.Shipment
	if err := handler.database.WithContext(context.Request.Context()).Order("updated_at DESC").Limit(100).Find(&shipments).Error; err != nil {
		context.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	context.HTML(http.StatusOK, "dashboard.tmpl", gin.H{"Shipments": shipments, "Now": time.Now().UTC()})
}

func (handler *Handler) respondError(context *gin.Context, err error) {
	requestID := middleware.CurrentRequestID(context)
	switch {
	case errors.Is(err, services.ErrInvalidShipment):
		httpapi.Error(context, http.StatusUnprocessableEntity, "INVALID_SHIPMENT", "Shipment payload is invalid", requestID)
	case errors.Is(err, services.ErrInvalidETA):
		httpapi.Error(context, http.StatusUnprocessableEntity, "INVALID_ESTIMATED_DELIVERY", "Estimated delivery range is invalid", requestID)
	case errors.Is(err, services.ErrInvalidEvent):
		httpapi.Error(context, http.StatusUnprocessableEntity, "INVALID_EVENT", "Shipment event payload is invalid", requestID)
	case errors.Is(err, services.ErrShipmentNotFound):
		httpapi.Error(context, http.StatusNotFound, "SHIPMENT_NOT_FOUND", "Shipment not found", requestID)
	case errors.Is(err, services.ErrReturnNotFound):
		httpapi.Error(context, http.StatusNotFound, "RETURN_NOT_FOUND", "Return shipment not found", requestID)
	case errors.Is(err, services.ErrInvalidTransition):
		httpapi.Error(context, http.StatusConflict, "INVALID_STATUS_TRANSITION", "Shipment status transition is invalid", requestID)
	case errors.Is(err, services.ErrIdempotencyConflict):
		httpapi.Error(context, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key was already used for a different request", requestID)
	case errors.Is(err, services.ErrForbidden):
		httpapi.Error(context, http.StatusForbidden, "FORBIDDEN", "Operation is not permitted for this role", requestID)
	default:
		httpapi.Error(context, http.StatusInternalServerError, "INTERNAL_ERROR", "Shipping request failed", requestID)
	}
}
