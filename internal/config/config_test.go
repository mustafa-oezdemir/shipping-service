package config

import (
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestValidateProductionPublicURL(t *testing.T) {
	gotURL, gotHost, err := validatePublicURL("APP_URL", "https://pehlione-shipping.com/", true)
	if err != nil || gotURL != "https://pehlione-shipping.com" || gotHost != "pehlione-shipping.com" {
		t.Fatalf("unexpected production URL: %q %q %v", gotURL, gotHost, err)
	}
	for _, raw := range []string{"http://pehlione-shipping.com", "https://user:pass@pehlione-shipping.com", "https://pehlione-shipping.com/path"} {
		if _, _, err := validatePublicURL("APP_URL", raw, true); err == nil {
			t.Errorf("expected %q to fail production validation", raw)
		}
	}
}

func TestValidateInternalServiceURLAllowsHTTP(t *testing.T) {
	if err := validateHTTPURL("ECOMMERCE_API_URL", "http://ecommerce-app:8080", false); err != nil {
		t.Fatalf("internal Docker URL should be accepted: %v", err)
	}
}

func TestResolveDatabaseDSNPrecedence(t *testing.T) {
	t.Setenv("DATABASE_DSN_DOCKER", "docker-dsn")
	t.Setenv("DATABASE_DSN", "local-dsn")

	got, err := resolveDatabaseDSN()
	if err != nil || got != "docker-dsn" {
		t.Fatalf("expected Docker DSN, got %q: %v", got, err)
	}

	t.Setenv("DATABASE_DSN_DOCKER", "")
	got, err = resolveDatabaseDSN()
	if err != nil || got != "local-dsn" {
		t.Fatalf("expected local DSN, got %q: %v", got, err)
	}
}

func TestResolveDatabaseDSNFromMySQLEnvironment(t *testing.T) {
	t.Setenv("DATABASE_DSN_DOCKER", "")
	t.Setenv("DATABASE_DSN", "")
	t.Setenv("MYSQL_HOST", "127.0.0.1")
	t.Setenv("MYSQL_PORT", "")
	t.Setenv("SHIPPING_DB_HOST_PORT", "3308")
	t.Setenv("MYSQL_DATABASE", "pehlione-shipping")
	t.Setenv("MYSQL_USER", "pehlione-shipping")
	t.Setenv("MYSQL_PASSWORD", "test-password")

	dsn, err := resolveDatabaseDSN()
	if err != nil {
		t.Fatalf("resolve fallback DSN: %v", err)
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse generated DSN: %v", err)
	}
	if parsed.User != "pehlione-shipping" || parsed.Passwd != "test-password" || parsed.Addr != "127.0.0.1:3308" || parsed.DBName != "pehlione-shipping" || !parsed.ParseTime || parsed.Loc.String() != "UTC" || parsed.Params["charset"] != "utf8mb4" {
		t.Fatalf("unexpected generated DSN configuration: user=%q addr=%q database=%q", parsed.User, parsed.Addr, parsed.DBName)
	}
	if strings.Contains(dsn, "shipping:password") {
		t.Fatal("generated DSN contains the legacy hard-coded fallback")
	}
}

func TestResolveDatabaseDSNRequiresFallbackCredentials(t *testing.T) {
	t.Setenv("DATABASE_DSN_DOCKER", "")
	t.Setenv("DATABASE_DSN", "")
	t.Setenv("MYSQL_USER", "pehlione-shipping")
	t.Setenv("MYSQL_PASSWORD", "")
	t.Setenv("MYSQL_DATABASE", "pehlione-shipping")

	_, err := resolveDatabaseDSN()
	if err == nil || !strings.Contains(err.Error(), "MYSQL_PASSWORD") {
		t.Fatalf("expected a clear MYSQL_PASSWORD error, got %v", err)
	}
}
