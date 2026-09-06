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
	appmetrics "github.com/mustafa-oezdemir/shipping-service/internal/metrics"
	"github.com/mustafa-oezdemir/shipping-service/internal/middleware"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/outbox"
	"github.com/mustafa-oezdemir/shipping-service/internal/portal"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"github.com/mustafa-oezdemir/shipping-service/web"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	appConfig, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	gin.SetMode(appConfig.GinMode)
	shippingDatabase, err := database.Open(appConfig.DatabaseDSN, appConfig.DatabaseConnectTimeout)
	if err != nil {
		log.Fatal(err)
	}
	if err := database.Migrate(shippingDatabase); err != nil {
		log.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	metrics := appmetrics.New(registry)
	appmetrics.SetDefault(metrics)
	sqlDatabase, err := shippingDatabase.DB()
	if err != nil {
		log.Fatal(err)
	}
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewDBStatsCollector(sqlDatabase, "shipping"),
		appmetrics.NewStateCollector(shippingDatabase),
	)
	metrics.HealthLive.Set(1)
	templates, err := web.ParseTemplates()
	if err != nil {
		log.Fatal(err)
	}
	shipmentService := services.NewShipmentService(shippingDatabase, appConfig.Warehouse, metrics)
	handler := handlers.New(shipmentService, shippingDatabase, appConfig.PublicBaseURL, metrics)
	if err := portal.BootstrapAdmin(shippingDatabase, appConfig.InitialAdminEmail, appConfig.InitialAdminPassword, appConfig.InitialAdminFirstName, appConfig.InitialAdminLastName); err != nil {
		log.Fatal(err)
	}
	portalHandler, err := portal.New(shippingDatabase, shipmentService, appConfig.SessionSecret, appConfig.ProfileImageDirectory, appConfig.PublicBaseURL, appConfig.AppEnv == "production", appConfig.SessionTTL)
	if err != nil {
		log.Fatal(err)
	}
	dispatcher := outbox.NewDispatcher(shippingDatabase, outbox.Config{CallbackURL: appConfig.EcommerceCallbackURL, CallbackToken: appConfig.EcommerceCallbackToken, PollInterval: appConfig.OutboxPollInterval, RetryBaseDelay: appConfig.OutboxRetryBaseDelay, ProcessingStaleAfter: appConfig.OutboxProcessingStaleAfter, MaxAttempts: appConfig.OutboxMaxAttempts, RequestTimeout: appConfig.RequestTimeout, ServiceName: "shipping-service"})

	router := gin.New()
	if err := router.SetTrustedProxies(appConfig.TrustedProxies); err != nil {
		log.Fatal(err)
	}
	router.Use(middleware.RequestMetadata("shipping-service"), middleware.Metrics(metrics), middleware.SecurityHeaders(appConfig.AppEnv == "production"), gin.Logger(), gin.Recovery())
	router.MaxMultipartMemory = 1 << 20
	router.SetHTMLTemplate(templates)
	router.Static("/static", "./web/static")
	router.GET("/", handler.Index)
	router.GET("/health", handler.Health)
	router.GET("/health/live", handler.Health)
	router.GET("/healthz", handler.Health)
	router.GET("/ready", handler.Ready)
	router.GET("/health/ready", handler.Ready)
	router.GET("/readyz", handler.Ready)
	router.GET("/track/:trackingNumber", handler.PublicTracking)
	router.GET("/qr/:trackingNumber", handler.TrackingQR)
	portalHandler.Register(router)

	internal := router.Group("/api/v1")
	internal.Use(middleware.RequireServiceToken(appConfig.ServiceTokens()...), middleware.NoStore())
	internal.POST("/shipments", middleware.RequireIdempotencyKey(), handler.CreateShipment)
	internal.GET("/shipments/order/:orderID", handler.GetShipmentForOrder)
	internal.GET("/shipments/:trackingNumber/events", handler.ShipmentEvents)
	internal.GET("/shipments/:trackingNumber", handler.GetShipmentByTracking)
	internal.POST("/shipments/:id/cancel", middleware.RequireIdempotencyKey(), handler.CancelShipment)
	internal.POST("/returns", middleware.RequireIdempotencyKey(), handler.CreateReturn)
	internal.GET("/returns/:trackingNumber", handler.GetReturnByTracking)

	operations := router.Group("")
	operations.Use(middleware.RequireServiceToken(appConfig.ServiceTokens()...), middleware.RequireRole(models.RoleShippingAdmin, models.RoleWarehouseEmployee, models.RoleDeliveryEmployee, models.RoleSupport), middleware.NoStore())
	operations.PATCH("/api/v1/shipments/:id/status", middleware.RequireIdempotencyKey(), handler.TransitionShipment)
	operations.PATCH("/api/v1/shipments/:id/eta", middleware.RequireIdempotencyKey(), handler.UpdateETA)
	operations.PATCH("/api/v1/shipments/:id/stops", middleware.RequireIdempotencyKey(), handler.UpdateStops)
	operations.POST("/api/v1/shipments/:id/events", middleware.RequireIdempotencyKey(), handler.CreateShipmentEvent)

	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go dispatcher.Run(rootContext)

	applicationServer := &http.Server{Addr: ":" + appConfig.AppPort, Handler: router, ReadHeaderTimeout: 5 * time.Second}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	metricsServer := &http.Server{Addr: ":" + appConfig.MetricsPort, Handler: metricsMux, ReadHeaderTimeout: 5 * time.Second}

	serverErrors := make(chan error, 2)
	go func() { serverErrors <- applicationServer.ListenAndServe() }()
	go func() { serverErrors <- metricsServer.ListenAndServe() }()
	select {
	case <-rootContext.Done():
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped unexpectedly: %v", err)
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = applicationServer.Shutdown(shutdownContext)
	_ = metricsServer.Shutdown(shutdownContext)
}
