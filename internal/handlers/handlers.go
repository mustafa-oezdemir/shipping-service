package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/middleware"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"github.com/skip2/go-qrcode"
	"gorm.io/gorm"
)

type Handler struct {
	service       *services.ShipmentService
	database      *gorm.DB
	publicBaseURL string
}

func New(service *services.ShipmentService, database *gorm.DB, publicBaseURL string) *Handler {
	return &Handler{service: service, database: database, publicBaseURL: strings.TrimRight(publicBaseURL, "/")}
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
	var input services.CreateShipmentInput
	if err := context.ShouldBindJSON(&input); err != nil {
		context.JSON(http.StatusBadRequest, gin.H{"error": "invalid shipment payload"})
		return
	}
	shipment, err := handler.service.Create(context.Request.Context(), input, context.GetHeader("Idempotency-Key"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	context.JSON(http.StatusCreated, shipment)
}

func (handler *Handler) CreateReturn(context *gin.Context) {
	var input services.CreateReturnInput
	if err := context.ShouldBindJSON(&input); err != nil {
		context.JSON(http.StatusBadRequest, gin.H{"error": "invalid return payload"})
		return
	}
	shipment, err := handler.service.CreateReturn(context.Request.Context(), input, context.GetHeader("Idempotency-Key"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	context.JSON(http.StatusCreated, shipment)
}

func (handler *Handler) GetShipmentForOrder(context *gin.Context) {
	var shipment models.Shipment
	if err := handler.database.WithContext(context.Request.Context()).Where("external_order_id = ?", context.Param("orderID")).First(&shipment).Error; err != nil {
		context.AbortWithStatus(http.StatusNotFound)
		return
	}
	context.JSON(http.StatusOK, shipment)
}

func (handler *Handler) TransitionShipment(context *gin.Context) {
	var request struct {
		ExpectedStatus models.ShipmentStatus `json:"expected_status" binding:"required"`
		Status         models.ShipmentStatus `json:"status" binding:"required"`
	}
	if err := context.ShouldBindJSON(&request); err != nil {
		context.JSON(http.StatusBadRequest, gin.H{"error": "expected_status and status are required"})
		return
	}
	id, err := strconv.ParseUint(context.Param("id"), 10, 64)
	if err != nil || id == 0 {
		context.AbortWithStatus(http.StatusNotFound)
		return
	}
	shipment, err := handler.service.Transition(context.Request.Context(), uint(id), request.ExpectedStatus, request.Status, middleware.CurrentRole(context), context.GetHeader("X-Actor-ID"), context.GetHeader("Idempotency-Key"))
	if err != nil {
		handler.respondError(context, err)
		return
	}
	context.JSON(http.StatusOK, shipment)
}

func (handler *Handler) UpdateStops(context *gin.Context) {
	var request struct {
		RemainingStops int `json:"remaining_stops" binding:"gte=0,lte=500"`
	}
	if err := context.ShouldBindJSON(&request); err != nil {
		context.JSON(http.StatusBadRequest, gin.H{"error": "remaining_stops must be between 0 and 500"})
		return
	}
	id, err := strconv.ParseUint(context.Param("id"), 10, 64)
	if err != nil || id == 0 {
		context.AbortWithStatus(http.StatusNotFound)
		return
	}
	if err := handler.service.UpdateStops(context.Request.Context(), uint(id), request.RemainingStops, middleware.CurrentRole(context), context.GetHeader("X-Actor-ID"), context.GetHeader("Idempotency-Key")); err != nil {
		handler.respondError(context, err)
		return
	}
	context.Status(http.StatusNoContent)
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
	trackingURL := handler.publicBaseURL + "/track/" + context.Param("trackingNumber")
	png, err := qrcode.Encode(trackingURL, qrcode.Medium, 256)
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
	context.HTML(http.StatusOK, "label.tmpl", gin.H{"Shipment": shipment, "TrackingURL": handler.publicBaseURL + "/track/" + shipment.TrackingNumber})
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
	switch err {
	case services.ErrInvalidShipment:
		context.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case services.ErrShipmentNotFound:
		context.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case services.ErrInvalidTransition:
		context.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case services.ErrForbidden:
		context.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	default:
		context.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("shipping request failed: %v", err)})
	}
}
