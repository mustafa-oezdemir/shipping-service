package handlers

import "testing"

func TestTrackingURLUsesConfiguredPublicOrigin(t *testing.T) {
	for _, test := range []struct{ name, origin, want string }{
		{name: "development", origin: "http://localhost:8090", want: "http://localhost:8090/track/PKG%20123%2FDE"},
		{name: "production target", origin: "https://pehlione-shipping.com", want: "https://pehlione-shipping.com/track/PKG%20123%2FDE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := &Handler{publicBaseURL: test.origin}
			if got := handler.trackingURL("PKG 123/DE"); got != test.want {
				t.Fatalf("unexpected tracking URL: %s", got)
			}
		})
	}
}
