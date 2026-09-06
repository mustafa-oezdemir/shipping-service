package database

import (
	"context"
	"fmt"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/migrations"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func Open(dsn string, connectTimeout time.Duration) (*gorm.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	delay := 250 * time.Millisecond
	var lastErr error
	for {
		database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{TranslateError: true})
		if err == nil {
			return database, nil
		}
		lastErr = err
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("open shipping database before timeout: %w", lastErr)
		case <-timer.C:
		}
		if delay < 2*time.Second {
			delay *= 2
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
		}
	}
}

func Migrate(database *gorm.DB) error {
	if err := migrations.Apply(database); err != nil {
		return fmt.Errorf("apply shipping migrations: %w", err)
	}
	return nil
}
