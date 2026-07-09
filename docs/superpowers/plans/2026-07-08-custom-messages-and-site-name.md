# Custom API-key Messages & Configurable Site Name Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the admin configure a custom expired message, a custom quota-exceeded message (applied globally to all API keys, shown in both the API error response and the `/check` portal), and a custom site name that replaces "Kiro-Go" across the admin UI and portal.

**Architecture:** Three new `Config` string fields with default-on-empty getters and one combined setter. `proxy/auth.go` reads the two message getters when rejecting expired/over-quota keys. The self-service `/v1/key/info` payload carries the resolved messages; the portal shows them. A public `GET /api/site` endpoint returns the site name; shared client JS on `index.html` + `portal.html` fetches it and rewrites the DOM (Approach A). The admin Settings tab gains three inputs, saved through the existing `/settings` endpoint.

**Tech Stack:** Go stdlib (`net/http`, `encoding/json`), existing config JSON persistence, vanilla JS admin panel + portal.

## Global Constraints

- Messages are GLOBAL (two config strings), not per-key. One quota message covers both token- and credit-limit rejection. IP-limit rejection messages are UNCHANGED and out of scope.
- Empty config value = built-in default. Defaults: SiteName `"Kiro-Go"`, ExpiredMessage `"API key expired"`, QuotaMessage `"quota exceeded"`. Expose as exported constants `DefaultSiteName`, `DefaultExpiredMessage`, `DefaultQuotaMessage`.
- HTTP status codes are UNCHANGED: expired = 401 `authentication_error`; quota = 429 `rate_limit_error`. Only the message string changes.
- Site name delivery is Approach A (public JSON endpoint + client JS DOM rewrite). No server-side HTML templating. `/api/site` is public (no auth) — it exposes only the display name.
- Admin config accessors follow the existing `cfgLock` + default-on-empty pattern (see `GetLogLevel`, `GetPublicBaseURL`). Setter trims and persists via `Save()`.
- Operator-provided strings are shown verbatim (not localized).
- Comments may be English or Chinese; match the surrounding file.

---

### Task 1: Config fields + accessors

**Files:**
- Modify: `config/config.go` (add 3 fields to `Config`, 3 constants, 3 getters, 1 setter)
- Test: `config/branding_test.go` (new)

**Interfaces:**
- Produces:
  - `Config` fields: `SiteName string`, `ExpiredMessage string`, `QuotaMessage string`
  - `const DefaultSiteName = "Kiro-Go"`, `DefaultExpiredMessage = "API key expired"`, `DefaultQuotaMessage = "quota exceeded"`
  - `func GetSiteName() string`, `func GetExpiredMessage() string`, `func GetQuotaMessage() string` (return default when stored value empty)
  - `func GetBrandingRaw() (siteName, expiredMessage, quotaMessage string)` (raw stored values, empty when unset — for the admin form)
  - `func UpdateBranding(siteName, expiredMessage, quotaMessage string) error`

- [ ] **Step 1: Add fields to the `Config` struct (`config/config.go`)**

In the `Config` struct, after the `PublicBaseURL string` field (around line 285), add:

```go
	// Branding / custom messaging (empty = built-in default; see Default* consts)
	SiteName       string `json:"siteName,omitempty"`       // replaces "Kiro-Go" in UI/portal
	ExpiredMessage string `json:"expiredMessage,omitempty"` // API-key expired rejection message
	QuotaMessage   string `json:"quotaMessage,omitempty"`   // API-key over-quota rejection message
```

- [ ] **Step 2: Write failing test `config/branding_test.go`**

```go
package config

import (
	"path/filepath"
	"testing"
)

func TestBrandingDefaults(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := GetSiteName(); got != DefaultSiteName {
		t.Fatalf("GetSiteName default = %q, want %q", got, DefaultSiteName)
	}
	if got := GetExpiredMessage(); got != DefaultExpiredMessage {
		t.Fatalf("GetExpiredMessage default = %q, want %q", got, DefaultExpiredMessage)
	}
	if got := GetQuotaMessage(); got != DefaultQuotaMessage {
		t.Fatalf("GetQuotaMessage default = %q, want %q", got, DefaultQuotaMessage)
	}
	sn, em, qm := GetBrandingRaw()
	if sn != "" || em != "" || qm != "" {
		t.Fatalf("GetBrandingRaw unset = (%q,%q,%q), want all empty", sn, em, qm)
	}
}

func TestUpdateBrandingRoundTrip(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := UpdateBranding("  My Proxy  ", " gone ", " no credits "); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := GetSiteName(); got != "My Proxy" {
		t.Fatalf("GetSiteName = %q, want trimmed %q", got, "My Proxy")
	}
	if got := GetExpiredMessage(); got != "gone" {
		t.Fatalf("GetExpiredMessage = %q, want %q", got, "gone")
	}
	if got := GetQuotaMessage(); got != "no credits" {
		t.Fatalf("GetQuotaMessage = %q, want %q", got, "no credits")
	}
	// Reload from disk and confirm persistence.
	if err := Init(cfgFile); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	if got := GetSiteName(); got != "My Proxy" {
		t.Fatalf("after reload GetSiteName = %q, want %q", got, "My Proxy")
	}
	// Empty values reset to defaults on read.
	if err := UpdateBranding("", "", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := GetSiteName(); got != DefaultSiteName {
		t.Fatalf("after clear GetSiteName = %q, want default %q", got, DefaultSiteName)
	}
}
```

- [ ] **Step 3: Run — expect FAIL (undefined symbols)**

Run: `go test ./config/ -run 'TestBranding|TestUpdateBranding' -v`
Expected: FAIL (undefined: `GetSiteName`, `DefaultSiteName`, etc.)

- [ ] **Step 4: Implement constants + accessors (`config/config.go`)**

Add near the other package constants (e.g. after `DefaultMaxPayloadBytes`, around line 1178):

```go
// Branding defaults, used when the corresponding Config field is empty.
const (
	DefaultSiteName       = "Kiro-Go"
	DefaultExpiredMessage = "API key expired"
	DefaultQuotaMessage   = "quota exceeded"
)
```

Add the accessors near the other getters/setters (e.g. after `UpdatePublicBaseURL`, around line 1232):

```go
// GetSiteName returns the configured site name, or DefaultSiteName when unset.
func GetSiteName() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil || cfg.SiteName == "" {
		return DefaultSiteName
	}
	return cfg.SiteName
}

// GetExpiredMessage returns the configured expired-key message, or the default when unset.
func GetExpiredMessage() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil || cfg.ExpiredMessage == "" {
		return DefaultExpiredMessage
	}
	return cfg.ExpiredMessage
}

// GetQuotaMessage returns the configured over-quota message, or the default when unset.
func GetQuotaMessage() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil || cfg.QuotaMessage == "" {
		return DefaultQuotaMessage
	}
	return cfg.QuotaMessage
}

// GetBrandingRaw returns the raw stored branding values (empty when unset), so the
// admin form can distinguish "unset" (show placeholder/default hint) from a value.
func GetBrandingRaw() (siteName, expiredMessage, quotaMessage string) {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return "", "", ""
	}
	return cfg.SiteName, cfg.ExpiredMessage, cfg.QuotaMessage
}

// UpdateBranding sets the site name and custom messages (each trimmed; empty means
// "use default") and persists the change.
func UpdateBranding(siteName, expiredMessage, quotaMessage string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.SiteName = strings.TrimSpace(siteName)
	cfg.ExpiredMessage = strings.TrimSpace(expiredMessage)
	cfg.QuotaMessage = strings.TrimSpace(quotaMessage)
	return Save()
}
```

(`strings` is already imported in `config/config.go`.)

- [ ] **Step 5: Run — expect PASS**

Run: `go test ./config/ -run 'TestBranding|TestUpdateBranding' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/branding_test.go
git commit -m "feat(config): add site name and custom message fields"
```

---

### Task 2: Custom messages in API error response (`proxy/auth.go`)

**Files:**
- Modify: `proxy/auth.go` (expired + over-limit branches in `authenticate`)
- Test: `proxy/auth_test.go` (append)

**Interfaces:**
- Consumes: `config.GetExpiredMessage()`, `config.GetQuotaMessage()`.

- [ ] **Step 1: Write failing tests (append to `proxy/auth_test.go`)**

```go
func TestAuthenticateCustomExpiredMessage(t *testing.T) {
	mustInitConfig(t)
	// expired 1h ago
	if _, err := config.AddApiKey(config.ApiKeyEntry{Name: "exp", Key: "sk-exp", Enabled: true, ExpiresAt: time.Now().Add(-time.Hour).Unix()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireAuth(t)
	if err := config.UpdateBranding("", "your key has expired, contact support", ""); err != nil {
		t.Fatalf("branding: %v", err)
	}

	h := &Handler{}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer sk-exp")
	_, err := h.authenticate(r)
	ae, ok := err.(*authError)
	if !ok {
		t.Fatalf("expected *authError, got %T", err)
	}
	if ae.status != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", ae.status)
	}
	if ae.message != "your key has expired, contact support" {
		t.Fatalf("expected custom expired message, got %q", ae.message)
	}
}

func TestAuthenticateCustomQuotaMessage(t *testing.T) {
	mustInitConfig(t)
	if _, err := config.AddApiKey(config.ApiKeyEntry{Name: "q", Key: "sk-q", Enabled: true, TokenLimit: 10, TokensUsed: 10}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireAuth(t)
	if err := config.UpdateBranding("", "", "out of credits, please top up"); err != nil {
		t.Fatalf("branding: %v", err)
	}

	h := &Handler{}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer sk-q")
	_, err := h.authenticate(r)
	ae, ok := err.(*authError)
	if !ok {
		t.Fatalf("expected *authError, got %T", err)
	}
	if ae.status != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", ae.status)
	}
	if ae.message != "out of credits, please top up" {
		t.Fatalf("expected custom quota message, got %q", ae.message)
	}
}

func TestAuthenticateDefaultMessagesWhenUnset(t *testing.T) {
	mustInitConfig(t)
	if _, err := config.AddApiKey(config.ApiKeyEntry{Name: "d", Key: "sk-d", Enabled: true, ExpiresAt: time.Now().Add(-time.Hour).Unix()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireAuth(t)
	h := &Handler{}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer sk-d")
	_, err := h.authenticate(r)
	ae, _ := err.(*authError)
	if ae == nil || ae.message != config.DefaultExpiredMessage {
		t.Fatalf("expected default expired message %q, got %v", config.DefaultExpiredMessage, err)
	}
}
```

Ensure the test file imports `"time"` (add if missing).

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./proxy/ -run 'TestAuthenticateCustom|TestAuthenticateDefaultMessages' -v`
Expected: FAIL (messages still hardcoded)

- [ ] **Step 3: Update the expired + over-limit branches (`proxy/auth.go`)**

Replace the expired branch:

```go
		if config.ApiKeyExpired(*entry) {
			return nil, newAuthError(http.StatusUnauthorized, "authentication_error", config.GetExpiredMessage())
		}
```

Replace the over-limit branch (collapse the two sub-branches to one message):

```go
		if overToken, overCredit := config.ApiKeyOverLimit(*entry); overToken || overCredit {
			return nil, newAuthError(http.StatusTooManyRequests, "rate_limit_error", config.GetQuotaMessage())
		}
```

Leave the IP-limit `EnforceAndRecordIP` block below it unchanged.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./proxy/ -run 'TestAuthenticateCustom|TestAuthenticateDefaultMessages' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add proxy/auth.go proxy/auth_test.go
git commit -m "feat(proxy): use configurable expired/quota messages in auth rejection"
```

---

### Task 3: Custom messages in self-service payload (`proxy/request_log.go`)

**Files:**
- Modify: `proxy/request_log.go` (`apiKeySelfInfo` struct + handler)
- Test: `proxy/request_log_branding_test.go` (new)

**Interfaces:**
- Consumes: `config.GetExpiredMessage()`, `config.GetQuotaMessage()`.
- Produces: self-info JSON fields `expiredMessage`, `quotaMessage`.

- [ ] **Step 1: Add fields to the `apiKeySelfInfo` struct (`proxy/request_log.go`)**

After the IP fields (`MaxTotalIPs int`) added by the prior feature, add:

```go
	ExpiredMessage string `json:"expiredMessage,omitempty"`
	QuotaMessage   string `json:"quotaMessage,omitempty"`
```

- [ ] **Step 2: Populate them in the `apiKeySelfInfo` handler (`proxy/request_log.go`)**

In the handler, in the `apiKeySelfInfo{...}` literal that is encoded to the response, add:

```go
		ExpiredMessage: config.GetExpiredMessage(),
		QuotaMessage:   config.GetQuotaMessage(),
```

- [ ] **Step 3: Write failing test `proxy/request_log_branding_test.go`**

```go
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
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./proxy/ -run TestSelfInfoCarriesCustomMessages -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add proxy/request_log.go proxy/request_log_branding_test.go
git commit -m "feat(proxy): expose custom expired/quota messages in key-info payload"
```

---

### Task 4: Public `/api/site` endpoint + settings read/write of branding

**Files:**
- Modify: `proxy/handler.go` (add public route `GET /api/site`; add a handler; extend `apiGetSettings` + `apiUpdateSettings`)
- Test: `proxy/site_endpoint_test.go` (new)

**Interfaces:**
- Consumes: `config.GetSiteName()`, `config.GetBrandingRaw()`, `config.UpdateBranding()`.
- Produces: public `GET /api/site` → `{"siteName": "..."}`; `/settings` GET adds `siteName`/`expiredMessage`/`quotaMessage` (raw), POST accepts the same three.

- [ ] **Step 1: Add the public route (`proxy/handler.go`)**

In `ServeHTTP`, next to the existing public `case path == "/api/event_logging/batch":` (around line 411), add:

```go
	case path == "/api/site":
		h.apiGetSite(w, r)
```

- [ ] **Step 2: Add the `apiGetSite` handler (`proxy/handler.go`)**

Place it near `apiGetSettings` (around line 3502):

```go
// apiGetSite is a PUBLIC (no-auth) endpoint returning only the display site name,
// so the login page and portal can rewrite their branding before authentication.
// It exposes no secrets.
func (h *Handler) apiGetSite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"siteName": config.GetSiteName()})
}
```

- [ ] **Step 3: Extend `apiGetSettings` (`proxy/handler.go`)**

Add the raw branding values to the returned map (so the admin form shows blanks when unset, not the defaults):

```go
func (h *Handler) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	siteName, expiredMessage, quotaMessage := config.GetBrandingRaw()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"apiKey":          config.GetApiKey(),
		"requireApiKey":   config.IsApiKeyRequired(),
		"port":            config.GetPort(),
		"host":            config.GetHost(),
		"allowOverUsage":  config.GetAllowOverUsage(),
		"maxPayloadBytes": config.GetMaxPayloadBytes(),
		"publicBaseURL":   config.GetPublicBaseURL(),
		"siteName":        siteName,
		"expiredMessage":  expiredMessage,
		"quotaMessage":    quotaMessage,
	})
}
```

- [ ] **Step 4: Extend `apiUpdateSettings` (`proxy/handler.go`)**

Add three pointer fields to the request struct (after `PublicBaseURL *string`):

```go
		SiteName       *string `json:"siteName,omitempty"`
		ExpiredMessage *string `json:"expiredMessage,omitempty"`
		QuotaMessage   *string `json:"quotaMessage,omitempty"`
```

After the `if req.PublicBaseURL != nil { ... }` block, add a branding update that
preserves any field not present in the request (read current raw values first):

```go
	if req.SiteName != nil || req.ExpiredMessage != nil || req.QuotaMessage != nil {
		curSite, curExp, curQuota := config.GetBrandingRaw()
		if req.SiteName != nil {
			curSite = *req.SiteName
		}
		if req.ExpiredMessage != nil {
			curExp = *req.ExpiredMessage
		}
		if req.QuotaMessage != nil {
			curQuota = *req.QuotaMessage
		}
		if err := config.UpdateBranding(curSite, curExp, curQuota); err != nil {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
	}
```

- [ ] **Step 5: Write failing test `proxy/site_endpoint_test.go`**

```go
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
```

- [ ] **Step 6: Run — expect FAIL then PASS**

Run: `go test ./proxy/ -run 'TestApiGetSite' -v`
Expected: FAIL before Steps 1-2, PASS after. Then `go build ./... && go vet ./...` clean.

- [ ] **Step 7: Commit**

```bash
git add proxy/handler.go proxy/site_endpoint_test.go
git commit -m "feat(proxy): public /api/site endpoint and branding in settings"
```

---

### Task 5: Admin Settings UI for branding (`web/index.html` + `web/app.js` + locales)

**Files:**
- Modify: `web/index.html` (add a branding card in the settings tab, near the proxy card ~line 370)
- Modify: `web/app.js` (`loadSettings` populate ~line 1505, add `saveBranding`, wire button ~line 4035)
- Modify: `web/locales/en.json`, `web/locales/vi.json`, `web/locales/zh.json`

**Interfaces:**
- Consumes: `/settings` GET fields `siteName`/`expiredMessage`/`quotaMessage`; `/settings` POST accepts them.

- [ ] **Step 1: Add the branding card (`web/index.html`)**

Immediately before the proxy settings card (the `<div class="card">` at ~line 370), insert:

```html
        <div class="card">
          <div class="card-header"><span class="card-title" data-i18n="settings.branding"></span></div>
          <div class="form-group">
            <label data-i18n="settings.siteName"></label>
            <input type="text" id="siteName" placeholder="Kiro-Go" autocomplete="off" />
            <small data-i18n="settings.siteNameHint"></small>
          </div>
          <div class="form-group">
            <label data-i18n="settings.expiredMessage"></label>
            <input type="text" id="expiredMessage" autocomplete="off" />
            <small data-i18n="settings.expiredMessageHint"></small>
          </div>
          <div class="form-group">
            <label data-i18n="settings.quotaMessage"></label>
            <input type="text" id="quotaMessage" autocomplete="off" />
            <small data-i18n="settings.quotaMessageHint"></small>
            <button class="btn btn-primary btn-sm" id="saveBrandingBtn" style="margin-top:0.5rem;"
              data-i18n="settings.saveBranding"></button>
          </div>
        </div>
```

- [ ] **Step 2: Populate on load (`web/app.js`, in `loadSettings`)**

After the `if ($('publicBaseURL')) $('publicBaseURL').value = d.publicBaseURL || '';` line (~1505), add:

```javascript
    if ($('siteName')) $('siteName').value = d.siteName || '';
    if ($('expiredMessage')) $('expiredMessage').value = d.expiredMessage || '';
    if ($('quotaMessage')) $('quotaMessage').value = d.quotaMessage || '';
```

- [ ] **Step 3: Add `saveBranding` (`web/app.js`, near `savePublicBaseURL` ~line 1607)**

```javascript
  async function saveBranding() {
    const payload = {
      siteName: $('siteName').value.trim(),
      expiredMessage: $('expiredMessage').value.trim(),
      quotaMessage: $('quotaMessage').value.trim()
    };
    try {
      const res = await api('/settings', { method: 'POST', body: JSON.stringify(payload) });
      const d = await res.json().catch(() => ({}));
      if (!res.ok || d.success === false) throw new Error(d.error || t('common.saveFailed'));
      toast(t('settings.brandingSaved'), 'success');
      applySiteName(payload.siteName || 'Kiro-Go');
    } catch (e) {
      toast((e && e.message) || t('common.saveFailed'), 'error');
    }
  }
```

(`applySiteName` is defined in Task 6; it updates the DOM. This call lets the
admin see the new name without a reload.)

- [ ] **Step 4: Wire the button (`web/app.js`, near line 4035)**

After the `savePublicBaseURLBtn` wiring, add:

```javascript
    const saveBrandingBtn = $('saveBrandingBtn');
    if (saveBrandingBtn) saveBrandingBtn.addEventListener('click', saveBranding);
```

- [ ] **Step 5: Add locale strings to all three locale files**

In `web/locales/en.json`, under the `settings` object, add:

```json
"branding": "Branding",
"siteName": "Site name",
"siteNameHint": "Replaces \"Kiro-Go\" in the UI and portal. Empty = default.",
"expiredMessage": "Expired-key message",
"expiredMessageHint": "Shown when an expired key is used. Empty = default.",
"quotaMessage": "Quota-exceeded message",
"quotaMessageHint": "Shown when a key is over its token/credit limit. Empty = default.",
"saveBranding": "Save branding",
"brandingSaved": "Branding saved"
```

Add the same keys to `web/locales/vi.json` and `web/locales/zh.json`, translated
to match each file's existing tone (Vietnamese and Simplified Chinese). Keep the
JSON valid (commas, no trailing comma).

- [ ] **Step 6: Verify JSON validity + build**

Run: `for f in web/locales/en.json web/locales/vi.json web/locales/zh.json; do python -c "import json;json.load(open('$f',encoding='utf-8'))" && echo "$f ok"; done`
Then `go build ./...` (embeds assets).
Expected: all locales `ok`, build succeeds.

- [ ] **Step 7: Commit**

```bash
git add web/index.html web/app.js web/locales
git commit -m "feat(web): admin branding settings (site name + custom messages)"
```

---

### Task 6: Site-name DOM rewrite + portal custom-message display

**Files:**
- Modify: `web/app.js` (add `applySiteName`, call it from `init`)
- Modify: `web/portal.html` (add site-name fetch/apply in the IIFE; show custom messages in `renderResult`)

**Interfaces:**
- Consumes: `GET /api/site` → `{siteName}`; portal `/v1/key/info` fields `expiredMessage`/`quotaMessage`.
- Produces: `applySiteName(name)` in `web/app.js` (used here and by Task 5's `saveBranding`).

- [ ] **Step 1: Add `applySiteName` + fetch in `web/app.js`**

Add this function near `init` (e.g. just above `async function init()` ~line 4141):

```javascript
  // applySiteName rewrites every place the hardcoded "Kiro-Go" name appears with
  // the configured name. Idempotent; safe to call repeatedly (e.g. after save).
  function applySiteName(name) {
    name = (name || '').trim() || 'Kiro-Go';
    document.title = name;
    document.querySelectorAll('.brand-text, .footer-title').forEach(function (el) {
      el.textContent = name;
    });
    document.querySelectorAll('.brand[aria-label]').forEach(function (el) {
      el.setAttribute('aria-label', name);
    });
    // Footer copyright: "© <year> Kiro-Go" — rewrite the trailing name only.
    var meta = document.querySelector('.footer-meta span');
    if (meta) {
      var yr = new Date().getFullYear();
      meta.innerHTML = '© <span id="footerYear">' + yr + '</span> ' + escapeHtml(name);
    }
  }

  async function loadSiteName() {
    try {
      const res = await fetch('/api/site');
      if (!res.ok) return;
      const d = await res.json();
      if (d && d.siteName) applySiteName(d.siteName);
    } catch (e) { /* keep default name on failure */ }
  }
```

- [ ] **Step 2: Call `loadSiteName` from `init` (`web/app.js`)**

Inside `init()`, after `applyTranslations();` (~line 4145), add:

```javascript
    loadSiteName();
```

It runs early and updates the DOM when the fetch resolves. (The `footerYear`
set later in `init` still works; `applySiteName` re-creates that span with the
year already filled.)

- [ ] **Step 3: Add site-name fetch to the portal (`web/portal.html`)**

Inside the IIFE, just before the final `applyLang();` at the end (~line 1039),
add:

```javascript
      // Rewrite the portal brand + title from the configured site name.
      (function () {
        fetch('/api/site').then(function (r) { return r.ok ? r.json() : null; }).then(function (d) {
          if (!d || !d.siteName) return;
          var name = d.siteName;
          // Title is "API Key — Kiro-Go"; replace the trailing brand segment.
          document.title = document.title.replace(/Kiro-Go/g, name);
          document.querySelectorAll('.p-brand b, b').forEach(function (el) {
            if (el.textContent.trim() === 'Kiro-Go') el.textContent = name;
          });
        }).catch(function () { /* keep default */ });
      })();
```

(If the brand `<b>Kiro-Go</b>` has a more specific selector in the file, prefer
that; the `.trim() === 'Kiro-Go'` guard keeps the generic `b` fallback safe.)

- [ ] **Step 4: Show custom messages in the portal `renderResult` (`web/portal.html`)**

The status pill currently shows a short localized reason. Add the operator's
custom message as a note below the pill. In `renderResult(d)`, after the pill
block (after `pill.textContent = reason;` ~line 844), add:

```javascript
        var note = $('statusNote');
        if (!note) {
          note = document.createElement('div');
          note.id = 'statusNote';
          note.className = 'p-status-note';
          pill.parentNode.appendChild(note);
        }
        var msg = '';
        if (!d.valid) {
          if (d.expired && d.expiredMessage) msg = d.expiredMessage;
          else if ((d.overToken || d.overCredit) && d.quotaMessage) msg = d.quotaMessage;
        }
        note.textContent = msg;
        note.style.display = msg ? '' : 'none';
```

Add a minimal style for `.p-status-note` in the portal's `<style>` block (match
the muted-text convention already used, e.g.):

```css
    .p-status-note { margin-top: 6px; font-size: 0.85rem; color: var(--muted, #9aa0a6); }
```

- [ ] **Step 5: Verify (build + manual)**

Run: `go build ./...` (embeds the static assets). Confirm no NUL/control bytes were introduced: `python -c "print(open('web/app.js','rb').read().count(b'\x00'), open('web/portal.html','rb').read().count(b'\x00'))"` → `0 0`.

Manual: run the server, set a site name + custom messages in admin Settings, save. Confirm the admin header/title/footer update without reload; open `/admin` in a fresh tab and the login brand shows the new name; open `/check`, enter an expired/over-limit key, and the custom message appears under the status pill.

- [ ] **Step 6: Commit**

```bash
git add web/app.js web/portal.html
git commit -m "feat(web): apply configurable site name and show custom messages in portal"
```

## Self-review notes

- Spec §1 config → Task 1. §2 API error response → Task 2. §3 self-service payload + display → Tasks 3 (payload) + 6 (display). §4 site name delivery (public endpoint + JS) → Tasks 4 (endpoint) + 6 (JS). §5 admin API + UI → Tasks 4 (API) + 5 (UI).
- Defaults are shared constants (Task 1) consumed by getters and tests — no drift.
- Per the user's global no-commit rule, do NOT run the `git commit` steps unless the user asks; implement and leave changes pending, batching into one commit on request.
