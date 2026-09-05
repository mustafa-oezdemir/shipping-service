package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	AppPort              string
	DatabaseDSN          string
	ServiceToken         string
	InternalQRSecret     string
	PublicBaseURL        string
	Warehouse            Warehouse
	RequestTimeout       time.Duration
	EcommerceCallbackURL string
}

type Warehouse struct {
	Name        string
	Company     string
	Street      string
	HouseNumber string
	PostalCode  string
	City        string
	CountryCode string
}

func Load() (Config, error) {
	config := Config{
		AppPort:              environment("APP_PORT", "8090"),
		DatabaseDSN:          strings.TrimSpace(os.Getenv("DATABASE_DSN")),
		ServiceToken:         strings.TrimSpace(os.Getenv("ECOMMERCE_SERVICE_TOKEN")),
		InternalQRSecret:     strings.TrimSpace(os.Getenv("INTERNAL_QR_SECRET")),
		PublicBaseURL:        environment("PUBLIC_BASE_URL", "http://localhost:8090"),
		RequestTimeout:       10 * time.Second,
		EcommerceCallbackURL: strings.TrimSpace(os.Getenv("ECOMMERCE_CALLBACK_URL")),
		Warehouse: Warehouse{
			Name:        strings.TrimSpace(os.Getenv("WAREHOUSE_NAME")),
			Company:     strings.TrimSpace(os.Getenv("WAREHOUSE_COMPANY")),
			Street:      strings.TrimSpace(os.Getenv("WAREHOUSE_STREET")),
			HouseNumber: strings.TrimSpace(os.Getenv("WAREHOUSE_HOUSE_NUMBER")),
			PostalCode:  strings.TrimSpace(os.Getenv("WAREHOUSE_POSTAL_CODE")),
			City:        strings.TrimSpace(os.Getenv("WAREHOUSE_CITY")),
			CountryCode: environment("WAREHOUSE_COUNTRY", "DE"),
		},
	}
	if config.DatabaseDSN == "" || config.ServiceToken == "" || len(config.ServiceToken) < 32 || len(config.InternalQRSecret) < 32 {
		return Config{}, errors.New("DATABASE_DSN, ECOMMERCE_SERVICE_TOKEN and a 32-character INTERNAL_QR_SECRET are required")
	}
	if config.Warehouse.Name == "" || config.Warehouse.Street == "" || config.Warehouse.HouseNumber == "" || config.Warehouse.PostalCode == "" || config.Warehouse.City == "" {
		return Config{}, errors.New("complete warehouse address configuration is required")
	}
	if len(config.Warehouse.CountryCode) != 2 {
		return Config{}, fmt.Errorf("WAREHOUSE_COUNTRY must be a two-letter ISO code")
	}
	return config, nil
}

func environment(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
