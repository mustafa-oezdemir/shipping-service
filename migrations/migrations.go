package migrations

import (
	"fmt"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"gorm.io/gorm"
)

type migration struct {
	version string
	apply   func(*gorm.DB) error
}

var orderedMigrations = []migration{
	{version: "000001_initial", apply: applyInitialSchema},
	{version: "000002_api_v1_contract", apply: applyAPIContractUpgrade},
	{version: "000003_personnel_portal", apply: applyPersonnelPortal},
}

func applyPersonnelPortal(transaction *gorm.DB) error {
	if err := transaction.AutoMigrate(&models.User{}, &models.BrowserSession{}); err != nil {
		return err
	}
	columns := []struct{ name, sql string }{
		{"actor_user_id", "ALTER TABLE audit_logs ADD COLUMN actor_user_id BIGINT UNSIGNED NULL AFTER shipment_id"},
		{"entity_type", "ALTER TABLE audit_logs ADD COLUMN entity_type VARCHAR(40) NULL AFTER action"},
		{"entity_id", "ALTER TABLE audit_logs ADD COLUMN entity_id VARCHAR(64) NULL AFTER entity_type"},
		{"old_value", "ALTER TABLE audit_logs ADD COLUMN old_value TEXT NULL AFTER entity_id"},
		{"new_value", "ALTER TABLE audit_logs ADD COLUMN new_value TEXT NULL AFTER old_value"},
		{"request_id", "ALTER TABLE audit_logs ADD COLUMN request_id VARCHAR(80) NULL AFTER new_value"},
	}
	for _, column := range columns {
		if err := ensureColumn(transaction, &models.AuditLog{}, column.name, column.sql); err != nil {
			return err
		}
	}
	for _, index := range []string{"ActorUserID", "EntityType", "EntityID", "RequestID"} {
		if err := ensureIndex(transaction, &models.AuditLog{}, index); err != nil {
			return err
		}
	}
	return nil
}

func Apply(database *gorm.DB) error {
	return database.Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version VARCHAR(64) PRIMARY KEY, applied_at DATETIME(3) NOT NULL)").Error; err != nil {
			return err
		}
		for _, current := range orderedMigrations {
			var count int64
			if err := transaction.Table("schema_migrations").Where("version = ?", current.version).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if err := current.apply(transaction); err != nil {
				return fmt.Errorf("%s: %w", current.version, err)
			}
			if err := transaction.Table("schema_migrations").Create(map[string]any{"version": current.version, "applied_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func applyInitialSchema(transaction *gorm.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS shipments (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			created_at DATETIME(3) NULL,
			updated_at DATETIME(3) NULL,
			deleted_at DATETIME(3) NULL,
			public_id VARCHAR(64) NOT NULL,
			shipment_number VARCHAR(64) NOT NULL,
			tracking_number VARCHAR(64) NOT NULL,
			external_order_id VARCHAR(64) NOT NULL,
			external_customer_id VARCHAR(64) NOT NULL,
			business_key VARCHAR(191) NOT NULL,
			source_service VARCHAR(80) NOT NULL,
			idempotency_key VARCHAR(128) NOT NULL,
			original_shipment_id BIGINT UNSIGNED NULL,
			is_return BOOLEAN NOT NULL DEFAULT FALSE,
			status VARCHAR(50) NOT NULL,
			carrier VARCHAR(80) NOT NULL,
			service_level VARCHAR(80) NOT NULL,
			sender_first_name VARCHAR(255) NULL,
			sender_last_name VARCHAR(255) NULL,
			sender_company VARCHAR(255) NULL,
			sender_street VARCHAR(255) NULL,
			sender_house_number VARCHAR(255) NULL,
			sender_address_line2 VARCHAR(255) NULL,
			sender_postal_code VARCHAR(255) NULL,
			sender_city VARCHAR(255) NULL,
			sender_state VARCHAR(255) NULL,
			sender_country_code VARCHAR(255) NULL,
			sender_phone VARCHAR(255) NULL,
			recipient_first_name VARCHAR(255) NULL,
			recipient_last_name VARCHAR(255) NULL,
			recipient_company VARCHAR(255) NULL,
			recipient_street VARCHAR(255) NULL,
			recipient_house_number VARCHAR(255) NULL,
			recipient_address_line2 VARCHAR(255) NULL,
			recipient_postal_code VARCHAR(255) NULL,
			recipient_city VARCHAR(255) NULL,
			recipient_state VARCHAR(255) NULL,
			recipient_country_code VARCHAR(255) NULL,
			recipient_phone VARCHAR(255) NULL,
			estimated_from DATETIME(3) NULL,
			estimated_until DATETIME(3) NULL,
			delivered_at DATETIME(3) NULL,
			current_stop VARCHAR(255) NULL,
			remaining_stops BIGINT NULL,
			version BIGINT UNSIGNED NOT NULL DEFAULT 1,
			UNIQUE KEY uq_shipments_public_id (public_id),
			UNIQUE KEY uq_shipments_shipment_number (shipment_number),
			UNIQUE KEY uq_shipments_tracking_number (tracking_number),
			UNIQUE KEY uq_shipments_business_key (business_key),
			UNIQUE KEY idx_shipments_source_idempotency (source_service, idempotency_key),
			KEY idx_shipments_external_order_id (external_order_id),
			KEY idx_shipments_external_customer_id (external_customer_id),
			KEY idx_shipments_original_shipment_id (original_shipment_id),
			KEY idx_shipments_is_return (is_return),
			KEY idx_shipments_status (status),
			KEY idx_shipments_deleted_at (deleted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS shipment_items (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			created_at DATETIME(3) NULL,
			updated_at DATETIME(3) NULL,
			deleted_at DATETIME(3) NULL,
			shipment_id BIGINT UNSIGNED NOT NULL,
			external_product_id VARCHAR(64) NOT NULL,
			name VARCHAR(255) NOT NULL,
			sku VARCHAR(100) NULL,
			quantity BIGINT NOT NULL,
			KEY idx_shipment_items_shipment_id (shipment_id),
			KEY idx_shipment_items_deleted_at (deleted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS shipment_events (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			created_at DATETIME(3) NULL,
			updated_at DATETIME(3) NULL,
			deleted_at DATETIME(3) NULL,
			event_id VARCHAR(64) NOT NULL,
			shipment_id BIGINT UNSIGNED NOT NULL,
			source_service VARCHAR(80) NOT NULL,
			idempotency_key VARCHAR(128) NOT NULL,
			request_id VARCHAR(80) NOT NULL,
			event_type VARCHAR(64) NOT NULL,
			status VARCHAR(50) NOT NULL,
			title VARCHAR(255) NOT NULL,
			description TEXT NULL,
			location_name VARCHAR(255) NULL,
			city VARCHAR(120) NULL,
			postal_code VARCHAR(20) NULL,
			country_code VARCHAR(2) NULL,
			remaining_stops BIGINT NULL,
			estimated_from DATETIME(3) NULL,
			estimated_until DATETIME(3) NULL,
			occurred_at DATETIME(3) NOT NULL,
			created_by_type VARCHAR(40) NOT NULL,
			created_by_id VARCHAR(64) NULL,
			UNIQUE KEY uq_shipment_events_event_id (event_id),
			UNIQUE KEY idx_shipment_events_source_idempotency (source_service, idempotency_key),
			KEY idx_shipment_events_shipment_id (shipment_id),
			KEY idx_shipment_events_request_id (request_id),
			KEY idx_shipment_events_status (status),
			KEY idx_shipment_events_occurred_at (occurred_at),
			KEY idx_shipment_events_deleted_at (deleted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			created_at DATETIME(3) NULL,
			updated_at DATETIME(3) NULL,
			deleted_at DATETIME(3) NULL,
			shipment_id BIGINT UNSIGNED NOT NULL,
			actor_type VARCHAR(40) NOT NULL,
			actor_id VARCHAR(64) NULL,
			action VARCHAR(80) NOT NULL,
			old_status VARCHAR(50) NULL,
			new_status VARCHAR(50) NULL,
			KEY idx_audit_logs_shipment_id (shipment_id),
			KEY idx_audit_logs_deleted_at (deleted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			created_at DATETIME(3) NULL,
			updated_at DATETIME(3) NULL,
			deleted_at DATETIME(3) NULL,
			event_id VARCHAR(64) NOT NULL,
			shipment_id BIGINT UNSIGNED NOT NULL,
			shipment_event_id BIGINT UNSIGNED NOT NULL,
			request_id VARCHAR(80) NOT NULL,
			event_type VARCHAR(80) NOT NULL,
			payload JSON NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			attempts BIGINT NOT NULL DEFAULT 0,
			last_error VARCHAR(255) NULL,
			next_attempt_at DATETIME(3) NOT NULL,
			delivered_at DATETIME(3) NULL,
			UNIQUE KEY uq_outbox_events_event_id (event_id),
			UNIQUE KEY uq_outbox_events_shipment_event_id (shipment_event_id),
			KEY idx_outbox_events_shipment_id (shipment_id),
			KEY idx_outbox_events_request_id (request_id),
			KEY idx_outbox_events_status (status),
			KEY idx_outbox_events_next_attempt_at (next_attempt_at),
			KEY idx_outbox_events_deleted_at (deleted_at)
		)`,
	}
	for _, statement := range statements {
		if err := transaction.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func applyAPIContractUpgrade(transaction *gorm.DB) error {
	if err := ensureColumn(transaction, &models.Shipment{}, "public_id", "ALTER TABLE shipments ADD COLUMN public_id VARCHAR(64) NOT NULL DEFAULT '' AFTER deleted_at"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.Shipment{}, "business_key", "ALTER TABLE shipments ADD COLUMN business_key VARCHAR(191) NOT NULL DEFAULT '' AFTER external_customer_id"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.Shipment{}, "source_service", "ALTER TABLE shipments ADD COLUMN source_service VARCHAR(80) NOT NULL DEFAULT 'ecommerce-gin' AFTER business_key"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.ShipmentEvent{}, "event_id", "ALTER TABLE shipment_events ADD COLUMN event_id VARCHAR(64) NOT NULL DEFAULT '' AFTER deleted_at"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.ShipmentEvent{}, "source_service", "ALTER TABLE shipment_events ADD COLUMN source_service VARCHAR(80) NOT NULL DEFAULT 'shipping-service' AFTER shipment_id"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.ShipmentEvent{}, "request_id", "ALTER TABLE shipment_events ADD COLUMN request_id VARCHAR(80) NOT NULL DEFAULT '' AFTER idempotency_key"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.ShipmentEvent{}, "estimated_from", "ALTER TABLE shipment_events ADD COLUMN estimated_from DATETIME(3) NULL AFTER remaining_stops"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.ShipmentEvent{}, "estimated_until", "ALTER TABLE shipment_events ADD COLUMN estimated_until DATETIME(3) NULL AFTER estimated_from"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.OutboxEvent{}, "event_id", "ALTER TABLE outbox_events ADD COLUMN event_id VARCHAR(64) NOT NULL DEFAULT '' AFTER deleted_at"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.OutboxEvent{}, "shipment_event_id", "ALTER TABLE outbox_events ADD COLUMN shipment_event_id BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER shipment_id"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.OutboxEvent{}, "request_id", "ALTER TABLE outbox_events ADD COLUMN request_id VARCHAR(80) NOT NULL DEFAULT '' AFTER shipment_event_id"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.OutboxEvent{}, "last_error", "ALTER TABLE outbox_events ADD COLUMN last_error VARCHAR(255) NULL AFTER attempts"); err != nil {
		return err
	}
	if err := ensureColumn(transaction, &models.OutboxEvent{}, "delivered_at", "ALTER TABLE outbox_events ADD COLUMN delivered_at DATETIME(3) NULL AFTER next_attempt_at"); err != nil {
		return err
	}
	if transaction.Migrator().HasIndex(&models.Shipment{}, "idx_shipments_idempotency_key") {
		if err := transaction.Migrator().DropIndex(&models.Shipment{}, "idx_shipments_idempotency_key"); err != nil {
			return err
		}
	}
	if transaction.Migrator().HasIndex(&models.Shipment{}, "idx_shipments_order_direction") {
		if err := transaction.Migrator().DropIndex(&models.Shipment{}, "idx_shipments_order_direction"); err != nil {
			return err
		}
	}
	if transaction.Migrator().HasIndex(&models.ShipmentEvent{}, "idx_shipment_events_idempotency_key") {
		if err := transaction.Migrator().DropIndex(&models.ShipmentEvent{}, "idx_shipment_events_idempotency_key"); err != nil {
			return err
		}
	}
	if err := ensureIndex(transaction, &models.Shipment{}, "idx_shipments_source_idempotency"); err != nil {
		return err
	}
	if err := ensureIndex(transaction, &models.Shipment{}, "BusinessKey"); err != nil {
		return err
	}
	if err := ensureIndex(transaction, &models.Shipment{}, "PublicID"); err != nil {
		return err
	}
	if err := ensureIndex(transaction, &models.ShipmentEvent{}, "idx_shipment_events_source_idempotency"); err != nil {
		return err
	}
	if err := ensureIndex(transaction, &models.ShipmentEvent{}, "EventID"); err != nil {
		return err
	}
	if err := ensureIndex(transaction, &models.OutboxEvent{}, "EventID"); err != nil {
		return err
	}
	if err := ensureIndex(transaction, &models.OutboxEvent{}, "ShipmentEventID"); err != nil {
		return err
	}
	updates := []string{
		`UPDATE shipments SET public_id = CONCAT('shp_', LPAD(id, 10, '0')) WHERE public_id = ''`,
		`UPDATE shipments SET business_key = CASE WHEN is_return = 1 AND original_shipment_id IS NOT NULL THEN CONCAT('return:', external_order_id, ':shp_', LPAD(original_shipment_id, 10, '0')) ELSE CONCAT('shipment:', external_order_id) END WHERE business_key = ''`,
		`UPDATE shipments SET source_service = 'ecommerce-gin' WHERE source_service = ''`,
		`UPDATE shipment_events SET event_id = CONCAT('evt_', LPAD(id, 10, '0')) WHERE event_id = ''`,
		`UPDATE shipment_events SET source_service = 'shipping-service' WHERE source_service = ''`,
		`UPDATE shipment_events SET request_id = CONCAT('req_migrated_', id) WHERE request_id = ''`,
		`UPDATE outbox_events SET event_id = CONCAT('evt_', LPAD(id, 10, '0')) WHERE event_id = ''`,
		`UPDATE outbox_events SET shipment_event_id = id WHERE shipment_event_id = 0`,
		`UPDATE outbox_events SET request_id = CONCAT('req_migrated_', id) WHERE request_id = ''`,
	}
	for _, statement := range updates {
		if err := transaction.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(transaction *gorm.DB, model any, column, statement string) error {
	if transaction.Migrator().HasColumn(model, column) {
		return nil
	}
	return transaction.Exec(statement).Error
}

func ensureIndex(transaction *gorm.DB, model any, index string) error {
	if transaction.Migrator().HasIndex(model, index) {
		return nil
	}
	return transaction.Migrator().CreateIndex(model, index)
}
