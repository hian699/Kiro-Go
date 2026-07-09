package proxy

import (
	"encoding/json"
	"kiro-go/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSelfInfoCarriesCustomMessages(t *testing.T) {
	mustInitConfig(t)
	if _, err := config.AddApiKey(config.ApiKeyEntry{Name: "s", Key: "sk-self", Enabled: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := config.UpdateBranding("", "gone", "no credits"); err != nil {
		t.Fatalf("branding: %v", err)
	}

	h := &Handler{}
	r := httptest.NewRequest(http.MethodGet, "/v1/key/info", nil)
	r.Header.Set("Authorization", "Bearer sk-self")
	w := httptest.NewRecorder()
	h.apiKeySelfInfo(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["expiredMessage"] != "gone" {
		t.Fatalf("expiredMessage = %v, want %q", resp["expiredMessage"], "gone")
	}
	if resp["quotaMessage"] != "no credits" {
		t.Fatalf("quotaMessage = %v, want %q", resp["quotaMessage"], "no credits")
	}
}
