package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsHTTPSRequest(t *testing.T) {
	t.Run("direct tls request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "https://example.com/auth", nil)
		req.TLS = &tls.ConnectionState{}
		if !isHTTPSRequest(req) {
			t.Fatalf("expected HTTPS for direct TLS request")
		}
	})

	t.Run("forwarded proto https", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		if !isHTTPSRequest(req) {
			t.Fatalf("expected HTTPS for forwarded proto https")
		}
	})

	t.Run("forwarded proto list uses first hop", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", nil)
		req.Header.Set("X-Forwarded-Proto", "https, http")
		if !isHTTPSRequest(req) {
			t.Fatalf("expected HTTPS for first forwarded proto hop")
		}
	})

	t.Run("plain http request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", nil)
		if isHTTPSRequest(req) {
			t.Fatalf("expected non-HTTPS for plain request")
		}
	})
}
