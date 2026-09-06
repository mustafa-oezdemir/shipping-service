package testutil

import (
	"strings"
	"testing"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func NewTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite test db: %v", err)
	}
	if err := database.AutoMigrate(&models.Shipment{}, &models.ShipmentItem{}, &models.ShipmentEvent{}, &models.AuditLog{}, &models.OutboxEvent{}); err != nil {
		t.Fatalf("migrate sqlite test db: %v", err)
	}
	return database
}
