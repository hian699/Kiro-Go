package proxy

import (
	"kiro-go/config"
	"net/http/httptest"
	"testing"
)

// TestResolvePublicBaseURL exercises the redirect-base resolution used to build OAuth
// redirect_uri values. Only the explicit config override is honored — auto-detect from the
// admin request Host is intentionally absent (the loopback server listens on a different
// port than the admin UI, so the request host names the wrong endpoint).
func TestResolvePublicBaseURL(t *testing.T) {
	mustInitConfig(t)

	t.Run("config override wins", func(t *testing.T) {
		if err := config.UpdatePublicBaseURL("https://azr.hian.software/"); err != nil {
			t.Fatalf("set base url: %v", err)
		}
		defer config.UpdatePublicBaseURL("")

		if got := resolvePublicBaseURL(); got != "https://azr.hian.software" {
			t.Fatalf("override: got %q, want trimmed config value", got)
		}
	})

	t.Run("unset yields empty base (falls back to localhost loopback)", func(t *testing.T) {
		if err := config.UpdatePublicBaseURL(""); err != nil {
			t.Fatalf("clear base url: %v", err)
		}
		if got := resolvePublicBaseURL(); got != "" {
			t.Fatalf("unset: got %q, want empty", got)
		}
	})
}

func TestSelfServiceBaseURLUsesAPIBaseURL(t *testing.T) {
	mustInitConfig(t)

	if err := config.UpdatePublicBaseURL("https://sso.example.com/"); err != nil {
		t.Fatalf("set public base url: %v", err)
	}
	if err := config.UpdateAPIBaseURL("https://api.example.com/"); err != nil {
		t.Fatalf("set api base url: %v", err)
	}
	defer config.UpdatePublicBaseURL("")
	defer config.UpdateAPIBaseURL("")

	req := httptest.NewRequest("GET", "https://admin.example.com/v1/key/info", nil)
	if got := selfServiceBaseURL(req); got != "https://api.example.com" {
		t.Fatalf("got %q, want API base URL", got)
	}
}

func TestSelfServiceBaseURLFallsBackToRequestHost(t *testing.T) {
	mustInitConfig(t)

	if err := config.UpdatePublicBaseURL("https://sso.example.com/"); err != nil {
		t.Fatalf("set public base url: %v", err)
	}
	if err := config.UpdateAPIBaseURL(""); err != nil {
		t.Fatalf("clear api base url: %v", err)
	}
	defer config.UpdatePublicBaseURL("")

	req := httptest.NewRequest("GET", "http://api.example.com/v1/key/info", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if got := selfServiceBaseURL(req); got != "https://api.example.com" {
		t.Fatalf("got %q, want request host fallback", got)
	}
}
