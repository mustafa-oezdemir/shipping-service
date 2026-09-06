package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/config"
	"github.com/mustafa-oezdemir/shipping-service/internal/database"
	"github.com/mustafa-oezdemir/shipping-service/internal/handlers"
	"github.com/mustafa-oezdemir/shipping-service/internal/middleware"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/outbox"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"github.com/mustafa-oezdemir/shipping-service/web"
)

func main() {
	appConfig, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	shippingDatabase, err := database.Open(appConfig.DatabaseDSN, appConfig.DatabaseConnectTimeout)
	if err != nil {
		log.Fatal(err)
	}
	if err := database.Migrate(shippingDatabase); err != nil {
		log.Fatal(err)
	}
	templates, err := web.ParseTemplates()
	if err != nil {
		log.Fatal(err)
	}
	handler := handlers.New(services.NewShipmentService(shippingDatabase, appConfig.Warehouse), shippingDatabase, appConfig.PublicBaseURL)
	dispatcher := outbox.NewDispatcher(shippingDatabase, outbox.Config{CallbackURL: appConfig.EcommerceCallbackURL, CallbackToken: appConfig.EcommerceCallbackToken, PollInterval: appConfig.OutboxPollInterval, RetryBaseDelay: appConfig.OutboxRetryBaseDelay, ProcessingStaleAfter: appConfig.OutboxProcessingStaleAfter, MaxAttempts: appConfig.OutboxMaxAttempts, RequestTimeout: appConfig.RequestTimeout, ServiceName: "shipping-service"})

	router := gin.New()
	router.Use(middleware.RequestMetadata("shipping-service"), gin.Logger(), gin.Recovery())
	router.MaxMultipartMemory = 1 << 20
	router.SetHTMLTemplate(templates)
	router.Static("/static", "./web/static")
	router.GET("/health", handler.Health)
	router.GET("/ready", handler.Ready)
	router.GET("/track/:trackingNumber", handler.PublicTracking)
	router.GET("/qr/:trackingNumber", handler.TrackingQR)

	internal := router.Group("/api/v1")
	internal.Use(middleware.RequireServiceToken(appConfig.ServiceTokens()...), middleware.NoStore())
	internal.POST("/shipments", middleware.RequireIdempotencyKey(), handler.CreateShipment)
	internal.GET("/shipments/order/:orderID", handler.GetShipmentForOrder)
	internal.GET("/shipments/:trackingNumber/events", handler.ShipmentEvents)
	internal.GET("/shipments/:trackingNumber", handler.GetShipmentByTracking)
	internal.POST("/returns", middleware.RequireIdempotencyKey(), handler.CreateReturn)
	internal.GET("/returns/:trackingNumber", handler.GetReturnByTracking)

	operations := router.Group("")
	operations.Use(middleware.RequireServiceToken(appConfig.ServiceTokens()...), middleware.RequireRole(models.RoleShippingAdmin, models.RoleWarehouseEmployee, models.RoleDeliveryEmployee, models.RoleSupport), middleware.NoStore())
	operations.GET("/dashboard", handler.Dashboard)
	operations.GET("/shipments/:id", handler.InternalShipment)
	operations.GET("/shipments/:id/label", handler.Label)
	operations.PATCH("/api/v1/shipments/:id/status", middleware.RequireIdempotencyKey(), handler.TransitionShipment)
	operations.PATCH("/api/v1/shipments/:id/eta", middleware.RequireIdempotencyKey(), handler.UpdateETA)
	operations.PATCH("/api/v1/shipments/:id/stops", middleware.RequireIdempotencyKey(), handler.UpdateStops)
	operations.POST("/api/v1/shipments/:id/events", middleware.RequireIdempotencyKey(), handler.CreateShipmentEvent)

	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go dispatcher.Run(rootContext)

	server := &http.Server{Addr: ":" + appConfig.AppPort, Handler: router, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-rootContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
