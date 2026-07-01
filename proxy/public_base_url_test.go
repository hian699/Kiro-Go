package proxy

import (
	"crypto/tls"
	"kiro-go/config"
	"net/http/httptest"
	"testing"
)

// TestResolvePublicBaseURL exercises the redirect-base resolution used to build OAuth
// redirect_uri values so SSO login works behind a reverse proxy / custom domain instead
// of being hard-coded to localhost.
func TestResolvePublicBaseURL(t *testing.T) {
	mustInitConfig(t)

	t.Run("config override wins", func(t *testing.T) {
		if err := config.UpdatePublicBaseURL("https://azr.hian.software/"); err != nil {
			t.Fatalf("set base url: %v", err)
		}
		defer config.UpdatePublicBaseURL("")

		r := httptest.NewRequest("POST", "/admin/api/auth/kiro-sso/start", nil)
		r.Host = "localhost:3128"
		if got := resolvePublicBaseURL(r); got != "https://azr.hian.software" {
			t.Fatalf("override: got %q, want trimmed config value", got)
		}
	})

	t.Run("x-forwarded headers", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/admin/api/auth/kiro-sso/start", nil)
		r.Host = "localhost:3128"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "azr.hian.software")
		if got := resolvePublicBaseURL(r); got != "https://azr.hian.software" {
			t.Fatalf("forwarded: got %q", got)
		}
	})

	t.Run("forwarded host comma list takes first", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/x", nil)
		r.Host = "localhost:3128"
		r.Header.Set("X-Forwarded-Host", "azr.hian.software, internal-lb:3128")
		r.Header.Set("X-Forwarded-Proto", "https, http")
		if got := resolvePublicBaseURL(r); got != "https://azr.hian.software" {
			t.Fatalf("comma list: got %q", got)
		}
	})

	t.Run("falls back to request host without forwarding", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/x", nil)
		r.Host = "kiro.example:8080"
		if got := resolvePublicBaseURL(r); got != "http://kiro.example:8080" {
			t.Fatalf("host fallback: got %q", got)
		}
	})

	t.Run("https inferred from TLS", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/x", nil)
		r.Host = "kiro.example"
		r.TLS = &tls.ConnectionState{}
		if got := resolvePublicBaseURL(r); got != "https://kiro.example" {
			t.Fatalf("tls scheme: got %q", got)
		}
	})

	t.Run("empty host yields empty base", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/x", nil)
		r.Host = ""
		if got := resolvePublicBaseURL(r); got != "" {
			t.Fatalf("empty host: got %q, want empty", got)
		}
	})
}
