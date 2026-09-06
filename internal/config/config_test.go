package config

import "testing"

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
