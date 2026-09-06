package handlers

import "testing"

func TestTrackingURLUsesConfiguredProductionOrigin(t *testing.T) {
	handler := &Handler{publicBaseURL: "https://pehlione-shipping.com"}
	if got := handler.trackingURL("PKG 123/DE"); got != "https://pehlione-shipping.com/track/PKG%20123%2FDE" {
		t.Fatalf("unexpected tracking URL: %s", got)
	}
}
