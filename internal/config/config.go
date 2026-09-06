package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type Config struct {
	AppEnv                     string
	AppPort                    string
	MetricsPort                string
	AppURL                     string
	GinMode                    string
	TrustedProxies             []string
	DatabaseDSN                string
	DatabaseConnectTimeout     time.Duration
	CurrentServiceToken        string
	PreviousServiceToken       string
	InternalQRSecret           string
	PublicBaseURL              string
	EcommerceAPIURL            string
	EcommercePublicURL         string
	Warehouse                  Warehouse
	RequestTimeout             time.Duration
	EcommerceCallbackURL       string
	EcommerceCallbackToken     string
	OutboxPollInterval         time.Duration
	OutboxRetryBaseDelay       time.Duration
	OutboxProcessingStaleAfter time.Duration
	OutboxMaxAttempts          int
	SessionSecret              string
	SessionTTL                 time.Duration
	ProfileImageDirectory      string
	InitialAdminEmail          string
	InitialAdminPassword       string
	InitialAdminFirstName      string
	InitialAdminLastName       string
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
	databaseDSN, err := resolveDatabaseDSN()
	if err != nil {
		return Config{}, err
	}
	appEnv := strings.ToLower(environment("APP_ENV", "development"))
	appURL := firstNonEmptyEnv("APP_URL", "PUBLIC_BASE_URL")
	if appURL == "" {
		appURL = "http://localhost:8090"
	}
	validatedAppURL, _, err := validatePublicURL("APP_URL", appURL, appEnv == "production")
	if err != nil {
		return Config{}, err
	}
	ecommerceAPIURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ECOMMERCE_API_URL")), "/")
	ecommercePublicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ECOMMERCE_PUBLIC_URL")), "/")
	if ecommerceAPIURL != "" {
		if err := validateHTTPURL("ECOMMERCE_API_URL", ecommerceAPIURL, false); err != nil {
			return Config{}, err
		}
	}
	if ecommercePublicURL != "" {
		if _, _, err := validatePublicURL("ECOMMERCE_PUBLIC_URL", ecommercePublicURL, appEnv == "production"); err != nil {
			return Config{}, err
		}
	}
	callbackURL := strings.TrimSpace(os.Getenv("ECOMMERCE_CALLBACK_URL"))
	if callbackURL == "" && ecommerceAPIURL != "" {
		callbackURL = ecommerceAPIURL + "/api/v1/internal/shipping/events"
	}
	if callbackURL != "" {
		if err := validateHTTPURL("ECOMMERCE_CALLBACK_URL", callbackURL, true); err != nil {
			return Config{}, err
		}
	}
	config := Config{
		AppEnv:                     appEnv,
		AppPort:                    environment("APP_PORT", "8090"),
		MetricsPort:                environment("METRICS_PORT", "9092"),
		AppURL:                     validatedAppURL,
		GinMode:                    strings.ToLower(environment("GIN_MODE", map[bool]string{true: "release", false: "debug"}[appEnv == "production"])),
		TrustedProxies:             csvEnvironment("TRUSTED_PROXIES"),
		DatabaseDSN:                databaseDSN,
		DatabaseConnectTimeout:     durationEnvironment("DATABASE_CONNECT_TIMEOUT", 30*time.Second),
		CurrentServiceToken:        firstNonEmptyEnv("ECOMMERCE_TO_SHIPPING_TOKEN", "ECOMMERCE_SERVICE_TOKEN"),
		PreviousServiceToken:       strings.TrimSpace(os.Getenv("ECOMMERCE_TO_SHIPPING_PREVIOUS_TOKEN")),
		InternalQRSecret:           strings.TrimSpace(os.Getenv("INTERNAL_QR_SECRET")),
		PublicBaseURL:              validatedAppURL,
		EcommerceAPIURL:            ecommerceAPIURL,
		EcommercePublicURL:         ecommercePublicURL,
		RequestTimeout:             durationEnvironment("REQUEST_TIMEOUT", 10*time.Second),
		EcommerceCallbackURL:       callbackURL,
		EcommerceCallbackToken:     firstNonEmptyEnv("SHIPPING_TO_ECOMMERCE_TOKEN", "ECOMMERCE_CALLBACK_TOKEN"),
		OutboxPollInterval:         durationEnvironment("OUTBOX_POLL_INTERVAL", 5*time.Second),
		OutboxRetryBaseDelay:       durationEnvironment("OUTBOX_RETRY_BASE_DELAY", 30*time.Second),
		OutboxProcessingStaleAfter: durationEnvironment("OUTBOX_PROCESSING_STALE_AFTER", 2*time.Minute),
		OutboxMaxAttempts:          intEnvironment("OUTBOX_MAX_ATTEMPTS", 8),
		SessionSecret:              strings.TrimSpace(os.Getenv("SESSION_SECRET")),
		SessionTTL:                 durationEnvironment("SESSION_TTL", 12*time.Hour),
		ProfileImageDirectory:      environment("PROFILE_IMAGE_DIRECTORY", "./data/profile-images"),
		InitialAdminEmail:          strings.ToLower(strings.TrimSpace(os.Getenv("INITIAL_ADMIN_EMAIL"))),
		InitialAdminPassword:       os.Getenv("INITIAL_ADMIN_PASSWORD"),
		InitialAdminFirstName:      environment("INITIAL_ADMIN_FIRST_NAME", "Shipping"),
		InitialAdminLastName:       environment("INITIAL_ADMIN_LAST_NAME", "Administrator"),
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
	if config.AppEnv != "development" && config.AppEnv != "test" && config.AppEnv != "production" {
		return Config{}, errors.New("APP_ENV must be one of: development, test, production")
	}
	if config.AppEnv == "production" && strings.TrimSpace(os.Getenv("APP_URL")) == "" {
		return Config{}, errors.New("APP_URL is required in production")
	}
	if config.GinMode != "debug" && config.GinMode != "release" && config.GinMode != "test" {
		return Config{}, errors.New("GIN_MODE must be debug, release, or test")
	}
	if config.AppEnv == "production" && config.GinMode != "release" {
		return Config{}, errors.New("GIN_MODE must be release in production")
	}
	if len(config.CurrentServiceToken) < 32 || len(config.InternalQRSecret) < 32 {
		return Config{}, errors.New("ECOMMERCE_TO_SHIPPING_TOKEN (or ECOMMERCE_SERVICE_TOKEN) and a 32-character INTERNAL_QR_SECRET are required")
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
	if len(config.SessionSecret) < 32 {
		return Config{}, errors.New("SESSION_SECRET must be at least 32 characters")
	}
	if config.SessionTTL < 15*time.Minute || config.SessionTTL > 7*24*time.Hour {
		return Config{}, errors.New("SESSION_TTL must be between 15m and 168h")
	}
	if (config.InitialAdminEmail == "") != (config.InitialAdminPassword == "") {
		return Config{}, errors.New("INITIAL_ADMIN_EMAIL and INITIAL_ADMIN_PASSWORD must be set together")
	}
	return config, nil
}

func resolveDatabaseDSN() (string, error) {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_DSN_DOCKER")); dsn != "" {
		return dsn, nil
	}
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_DSN")); dsn != "" {
		return dsn, nil
	}

	user := strings.TrimSpace(os.Getenv("MYSQL_USER"))
	password := os.Getenv("MYSQL_PASSWORD")
	databaseName := strings.TrimSpace(os.Getenv("MYSQL_DATABASE"))
	if user == "" || strings.TrimSpace(password) == "" || databaseName == "" {
		return "", errors.New("database configuration requires MYSQL_USER, MYSQL_PASSWORD, and MYSQL_DATABASE when DATABASE_DSN_DOCKER and DATABASE_DSN are unset")
	}

	host := environment("MYSQL_HOST", "127.0.0.1")
	port := firstNonEmptyEnv("MYSQL_PORT", "SHIPPING_DB_HOST_PORT")
	if port == "" {
		port = "3306"
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return "", errors.New("MYSQL_PORT (or SHIPPING_DB_HOST_PORT) must be a valid TCP port")
	}

	mysqlConfig := mysql.Config{
		User:      user,
		Passwd:    password,
		Net:       "tcp",
		Addr:      net.JoinHostPort(host, port),
		DBName:    databaseName,
		ParseTime: true,
		Loc:       time.UTC,
		Params:    map[string]string{"charset": "utf8mb4"},
	}
	return mysqlConfig.FormatDSN(), nil
}

func validatePublicURL(name, raw string, requireHTTPS bool) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("%s must be an absolute http(s) origin without credentials, query, or fragment", name)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", "", fmt.Errorf("%s must not contain a path", name)
	}
	if requireHTTPS && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("%s must use https in production", name)
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), parsed.Host, nil
}

func validateHTTPURL(name, raw string, allowPath bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must be an absolute http(s) URL without credentials, query, or fragment", name)
	}
	if !allowPath && parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("%s must not contain a path", name)
	}
	return nil
}

func csvEnvironment(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
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
