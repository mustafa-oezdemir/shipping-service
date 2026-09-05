package migrations

import (
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"gorm.io/gorm"
)

const initialVersion = "000001_initial"

func Apply(database *gorm.DB) error {
	return database.Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version VARCHAR(64) PRIMARY KEY, applied_at DATETIME(3) NOT NULL)").Error; err != nil {
			return err
		}
		var count int64
		if err := transaction.Table("schema_migrations").Where("version = ?", initialVersion).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		if err := transaction.AutoMigrate(&models.Shipment{}, &models.ShipmentItem{}, &models.ShipmentEvent{}, &models.AuditLog{}, &models.OutboxEvent{}); err != nil {
			return err
		}
		return transaction.Table("schema_migrations").Create(map[string]any{"version": initialVersion, "applied_at": time.Now().UTC()}).Error
	})
}
