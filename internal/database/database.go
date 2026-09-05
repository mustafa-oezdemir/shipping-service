package database

import (
	"fmt"

	"github.com/mustafa-oezdemir/shipping-service/migrations"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func Open(dsn string) (*gorm.DB, error) {
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open shipping database: %w", err)
	}
	return database, nil
}

func Migrate(database *gorm.DB) error {
	if err := migrations.Apply(database); err != nil {
		return fmt.Errorf("apply shipping migrations: %w", err)
	}
	return nil
}
