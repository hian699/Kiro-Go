package proxy

import (
	"encoding/json"
	"kiro-go/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApiGetSiteDefault(t *testing.T) {
	mustInitConfig(t)
	h := &Handler{}
	w := httptest.NewRecorder()
	h.apiGetSite(w, httptest.NewRequest(http.MethodGet, "/api/site", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["siteName"] != config.DefaultSiteName {
		t.Fatalf("siteName = %q, want default %q", resp["siteName"], config.DefaultSiteName)
	}
}

func TestApiGetSiteConfigured(t *testing.T) {
	mustInitConfig(t)
	if err := config.UpdateBranding("My Proxy", "", ""); err != nil {
		t.Fatalf("branding: %v", err)
	}
	h := &Handler{}
	w := httptest.NewRecorder()
	h.apiGetSite(w, httptest.NewRequest(http.MethodGet, "/api/site", nil))
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["siteName"] != "My Proxy" {
		t.Fatalf("siteName = %q, want %q", resp["siteName"], "My Proxy")
	}
}
