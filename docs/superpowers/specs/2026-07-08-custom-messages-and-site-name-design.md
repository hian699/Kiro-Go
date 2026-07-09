# Custom API-key messages & configurable site name — design

Date: 2026-07-08

## Problem

Three operator-facing strings are currently hardcoded and cannot be customized:

1. The error message returned when an API key is **expired** (`proxy/auth.go` →
   `"API key expired"`).
2. The error message returned when an API key is **over quota** (token or credit
   limit) (`proxy/auth.go` → `"token limit exceeded"` / `"credit limit
   exceeded"`).
3. The **site name** "Kiro-Go", hardcoded across `web/index.html` (page title,
   header brand, footer, aria-label) and `web/portal.html` (title, brand).

An operator reselling API access wants to brand the deployment and give
customers a clear, custom explanation when their key stops working.

## Goal

Let the admin configure, from the admin panel:

- A custom **expired** message and a custom **quota-exceeded** message, applied
  globally to all API keys. These appear both in the API error response the
  client receives (401 / 429) and on the self-service portal (`/check`).
- A custom **site name** that replaces "Kiro-Go" everywhere it is shown: the
  admin page title/header/footer and the portal title/brand.

When a field is left blank, the current default behavior is preserved (default
English message / "Kiro-Go").

## Decisions locked (from user)

- **Messages are global** — two config strings, not per-key. One quota message
  covers every quota type (token and credit); IP-limit rejection keeps its own
  distinct messages and is out of scope here.
- **Messages appear in both places** — the API error response (status codes
  unchanged: 401 expired, 429 quota) and the portal `/check`.
- **One site name everywhere** — a single string replaces "Kiro-Go" in both the
  admin UI and the portal.
- **Site name delivery = Approach A** — a public JSON endpoint plus client-side
  JS that updates the DOM. No server-side templating; static files stay served
  as-is. Accepted trade-off: a brief flash of the default name before the fetch
  resolves, minimized by fetching and applying early.

## Components

### 1. Config fields + accessors (`config/config.go`)

Add to `Config`:

```go
// Branding / custom messaging (empty = built-in default)
SiteName       string `json:"siteName,omitempty"`       // replaces "Kiro-Go"; empty = "Kiro-Go"
ExpiredMessage string `json:"expiredMessage,omitempty"` // empty = "API key expired"
QuotaMessage   string `json:"quotaMessage,omitempty"`   // empty = "quota exceeded"
```

Add getters that return the default when empty, and one combined setter:

```go
func GetSiteName() string        // "" -> "Kiro-Go"
func GetExpiredMessage() string  // "" -> "API key expired"
func GetQuotaMessage() string    // "" -> "quota exceeded"
func UpdateBranding(siteName, expiredMessage, quotaMessage string) error // persists all three
```

Getters follow the existing `cfgLock.RLock` + default-on-empty pattern (e.g.
`GetLogLevel`). The setter trims each argument and stores it verbatim (empty
allowed, meaning "use default"), then `Save()`.

Defaults are exported as constants so the API layer and getters share one
source:

```go
const (
	DefaultSiteName       = "Kiro-Go"
	DefaultExpiredMessage = "API key expired"
	DefaultQuotaMessage   = "quota exceeded"
)
```

### 2. API error response (`proxy/auth.go`)

In `authenticate`, within the `config.HasApiKeys()` block:

- The `config.ApiKeyExpired(*entry)` branch uses `config.GetExpiredMessage()`
  instead of the literal `"API key expired"`. Status 401, code
  `authentication_error` unchanged.
- The over-limit branch (both `overToken` and `overCredit`) uses
  `config.GetQuotaMessage()` instead of `"token limit exceeded"` /
  `"credit limit exceeded"`. Status 429, code `rate_limit_error` unchanged. The
  two sub-branches collapse to one message.

IP-limit rejections (`IPRejectForbidden` / concurrent / total) are untouched.

### 3. Self-service portal payload + display

- `apiKeySelfInfo` struct (`proxy/request_log.go`) gains two fields, populated
  from config with defaults resolved server-side:

  ```go
  ExpiredMessage string `json:"expiredMessage,omitempty"`
  QuotaMessage   string `json:"quotaMessage,omitempty"`
  ```

- `web/portal.html` `renderResult`: when the key is expired, show
  `d.expiredMessage`; when over token/credit limit, show `d.quotaMessage`. These
  replace the current default status text for those states. If a field is
  absent/empty, fall back to the existing localized text.

### 4. Site name delivery (Approach A)

**Public endpoint.** Add `GET /api/site` to `proxy/handler.go` routing (public,
no auth — it only exposes the display name, no secrets):

```json
{ "siteName": "..." }
```

Returns `config.GetSiteName()`.

**Client application.** A small shared script (inline in each page, or a tiny
`web/site-name.js` loaded by both) fetches `/api/site` on load and, when the
returned name differs from the default, updates:

- `document.title` — preserving any suffix (portal title is
  `"API Key — Kiro-Go"`, so replace the "Kiro-Go" segment, not the whole title).
- every `.brand-text` element (header brand appears twice in `index.html`).
- the footer brand (`.footer-title` and the `© <year> Kiro-Go` line in
  `index.html`).
- the portal brand (`<b>Kiro-Go</b>` in `portal.html`).

To minimize the flash, the fetch is issued as early as possible and the DOM
update is a single synchronous pass on response.

**Admin config UI.** A "Site name" text input plus the two message textareas in
the admin Settings tab (`web/index.html` + `web/app.js`), saved via a new admin
endpoint.

### 5. Admin API + UI wiring

- New admin endpoints in `handleAdminAPI` (password-gated, under `/admin/api/`):
  - `GET /branding` → `{ siteName, expiredMessage, quotaMessage }` (raw stored
    values, empty when unset, so the form shows blanks not defaults).
  - `POST /branding` → body `{ siteName, expiredMessage, quotaMessage }` →
    `config.UpdateBranding(...)`.
- `web/app.js`: `loadBranding()` populates the three inputs on settings load;
  `saveBranding()` posts them; wire a save button. Add locale keys (en/vi/zh)
  for the labels/hints.
- After a successful save, re-fetch `/api/site` (or reuse the returned value) so
  the admin's own header updates without reload.

## Testing

- `config/config_test.go` (or a new `config/branding_test.go`): getters return
  defaults when empty and stored values when set; `UpdateBranding` persists and
  round-trips through save/load.
- `proxy/auth_test.go`: with a custom expired message configured, an expired key
  yields a 401 whose message equals the custom string; with a custom quota
  message, an over-limit key yields 429 with the custom string. With no config,
  the default strings are returned (regression guard).
- `proxy` handler test: `GET /api/site` returns the configured name (and the
  default when unset).
- Portal display and site-name DOM update are verified manually (static JS, no
  JS test harness in this repo — matches existing convention).

## Out of scope

- Per-key custom messages (decided: global only).
- Separate messages per quota type or for IP-limit rejections.
- Logo, favicon, tagline, or theme customization (name only).
- Server-side HTML templating (Approach B) — rejected in favor of the public
  endpoint + JS.
- Localizing the operator-provided custom strings; they are shown verbatim as
  the admin typed them.
