package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppPort                    string
	DatabaseDSN                string
	DatabaseConnectTimeout     time.Duration
	CurrentServiceToken        string
	PreviousServiceToken       string
	InternalQRSecret           string
	PublicBaseURL              string
	Warehouse                  Warehouse
	RequestTimeout             time.Duration
	EcommerceCallbackURL       string
	EcommerceCallbackToken     string
	OutboxPollInterval         time.Duration
	OutboxRetryBaseDelay       time.Duration
	OutboxProcessingStaleAfter time.Duration
	OutboxMaxAttempts          int
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
		AppPort:                    environment("APP_PORT", "8090"),
		DatabaseDSN:                strings.TrimSpace(os.Getenv("DATABASE_DSN")),
		DatabaseConnectTimeout:     durationEnvironment("DATABASE_CONNECT_TIMEOUT", 30*time.Second),
		CurrentServiceToken:        firstNonEmptyEnv("ECOMMERCE_TO_SHIPPING_TOKEN", "ECOMMERCE_SERVICE_TOKEN"),
		PreviousServiceToken:       strings.TrimSpace(os.Getenv("ECOMMERCE_TO_SHIPPING_PREVIOUS_TOKEN")),
		InternalQRSecret:           strings.TrimSpace(os.Getenv("INTERNAL_QR_SECRET")),
		PublicBaseURL:              environment("PUBLIC_BASE_URL", "http://localhost:8090"),
		RequestTimeout:             durationEnvironment("REQUEST_TIMEOUT", 10*time.Second),
		EcommerceCallbackURL:       strings.TrimSpace(os.Getenv("ECOMMERCE_CALLBACK_URL")),
		EcommerceCallbackToken:     firstNonEmptyEnv("SHIPPING_TO_ECOMMERCE_TOKEN", "ECOMMERCE_CALLBACK_TOKEN"),
		OutboxPollInterval:         durationEnvironment("OUTBOX_POLL_INTERVAL", 5*time.Second),
		OutboxRetryBaseDelay:       durationEnvironment("OUTBOX_RETRY_BASE_DELAY", 30*time.Second),
		OutboxProcessingStaleAfter: durationEnvironment("OUTBOX_PROCESSING_STALE_AFTER", 2*time.Minute),
		OutboxMaxAttempts:          intEnvironment("OUTBOX_MAX_ATTEMPTS", 8),
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
	if config.DatabaseDSN == "" || len(config.CurrentServiceToken) < 32 || len(config.InternalQRSecret) < 32 {
		return Config{}, errors.New("DATABASE_DSN, ECOMMERCE_TO_SHIPPING_TOKEN (or ECOMMERCE_SERVICE_TOKEN), and a 32-character INTERNAL_QR_SECRET are required")
	}
	if config.PreviousServiceToken != "" && len(config.PreviousServiceToken) < 32 {
		return Config{}, errors.New("ECOMMERCE_TO_SHIPPING_PREVIOUS_TOKEN must be at least 32 characters when set")
	}
	if config.EcommerceCallbackURL != "" && len(config.EcommerceCallbackToken) < 32 {
		return Config{}, errors.New("SHIPPING_TO_ECOMMERCE_TOKEN must be at least 32 characters when ECOMMERCE_CALLBACK_URL is configured")
	}
	if config.Warehouse.Name == "" || config.Warehouse.Street == "" || config.Warehouse.HouseNumber == "" || config.Warehouse.PostalCode == "" || config.Warehouse.City == "" {
		return Config{}, errors.New("complete warehouse address configuration is required")
	}
	if len(config.Warehouse.CountryCode) != 2 {
		return Config{}, fmt.Errorf("WAREHOUSE_COUNTRY must be a two-letter ISO code")
	}
	if config.OutboxMaxAttempts < 1 {
		return Config{}, errors.New("OUTBOX_MAX_ATTEMPTS must be greater than zero")
	}
	return config, nil
}

func (config Config) ServiceTokens() []string {
	tokens := []string{config.CurrentServiceToken}
	if config.PreviousServiceToken != "" {
		tokens = append(tokens, config.PreviousServiceToken)
	}
	return tokens
}

func environment(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func durationEnvironment(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func intEnvironment(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
