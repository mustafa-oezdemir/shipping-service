package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/config"
	"github.com/mustafa-oezdemir/shipping-service/internal/database"
	"github.com/mustafa-oezdemir/shipping-service/internal/handlers"
	"github.com/mustafa-oezdemir/shipping-service/internal/middleware"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"github.com/mustafa-oezdemir/shipping-service/web"
)

func main() {
	appConfig, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	shippingDatabase, err := database.Open(appConfig.DatabaseDSN)
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
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.MaxMultipartMemory = 1 << 20
	router.SetHTMLTemplate(templates)
	router.Static("/static", "./web/static")
	router.GET("/health", handler.Health)
	router.GET("/ready", handler.Ready)
	router.GET("/track/:trackingNumber", handler.PublicTracking)
	router.GET("/qr/:trackingNumber", handler.TrackingQR)
	internal := router.Group("/api/v1")
	internal.Use(middleware.RequireServiceToken(appConfig.ServiceToken))
	internal.POST("/shipments", handler.CreateShipment)
	internal.POST("/returns", handler.CreateReturn)
	internal.GET("/shipments/order/:orderID", handler.GetShipmentForOrder)
	operations := router.Group("")
	operations.Use(middleware.RequireServiceToken(appConfig.ServiceToken), middleware.RequireRole(models.RoleShippingAdmin, models.RoleWarehouseEmployee, models.RoleDeliveryEmployee, models.RoleSupport))
	operations.GET("/dashboard", handler.Dashboard)
	operations.GET("/shipments/:id", handler.InternalShipment)
	operations.GET("/shipments/:id/label", handler.Label)
	operations.PATCH("/api/v1/shipments/:id/status", handler.TransitionShipment)
	operations.PATCH("/api/v1/shipments/:id/stops", handler.UpdateStops)
	if err := http.ListenAndServe(":"+appConfig.AppPort, router); err != nil {
		log.Fatal(err)
	}
}
