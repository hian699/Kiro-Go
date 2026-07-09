package proxy

import (
	"encoding/json"
	"io"
	"kiro-go/config"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func actualTokensUsed(e config.ApiKeyEntry) int64 {
	if e.ActualTokensUsed > 0 {
		return e.ActualTokensUsed
	}
	return e.TokensUsed
}

func actualCreditsUsed(e config.ApiKeyEntry) float64 {
	if e.ActualCreditsUsed > 0 {
		return e.ActualCreditsUsed
	}
	return e.CreditsUsed
}

// apiKeyView is the response payload for listing/inspecting API keys. The Key field
// is masked so admins can identify entries without exposing the secret.
type apiKeyView struct {
	ID                       string  `json:"id"`
	Name                     string  `json:"name,omitempty"`
	KeyMasked                string  `json:"keyMasked"`
	Enabled                  bool    `json:"enabled"`
	Migrated                 bool    `json:"migrated,omitempty"`
	CreatedAt                int64   `json:"createdAt"`
	LastUsedAt               int64   `json:"lastUsedAt,omitempty"`
	ExpiresAt                int64   `json:"expiresAt,omitempty"`
	TokenLimit               int64   `json:"tokenLimit,omitempty"`
	CreditLimit              float64 `json:"creditLimit,omitempty"`
	TokensUsed               int64   `json:"tokensUsed"`
	CreditsUsed              float64 `json:"creditsUsed"`
	ActualTokensUsed         int64   `json:"actualTokensUsed"`
	ActualCreditsUsed        float64 `json:"actualCreditsUsed"`
	RequestsCount            int64   `json:"requestsCount"`
	BillingMultiplierEnabled bool    `json:"billingMultiplierEnabled,omitempty"`
	TokenMultiplier          float64 `json:"tokenMultiplier"`
	CreditMultiplier         float64 `json:"creditMultiplier"`

	MaxConcurrentIPs int      `json:"maxConcurrentIps,omitempty"`
	MaxTotalIPs      int      `json:"maxTotalIps,omitempty"`
	IPAllowlist      []string `json:"ipAllowlist,omitempty"`
	ConcurrentIPs    int      `json:"concurrentIps"`
	TotalIPs         int      `json:"totalIps"`

	RPMLimit int   `json:"rpmLimit,omitempty"`
	TPMLimit int64 `json:"tpmLimit,omitempty"`
}

func toApiKeyView(e config.ApiKeyEntry) apiKeyView {
	conc, total := config.ApiKeyIPStats(e, ipActiveWindow)
	return apiKeyView{
		ID:                       e.ID,
		Name:                     e.Name,
		KeyMasked:                config.MaskApiKey(e.Key),
		Enabled:                  e.Enabled,
		Migrated:                 e.Migrated,
		CreatedAt:                e.CreatedAt,
		LastUsedAt:               e.LastUsedAt,
		ExpiresAt:                e.ExpiresAt,
		TokenLimit:               e.TokenLimit,
		CreditLimit:              e.CreditLimit,
		TokensUsed:               e.TokensUsed,
		CreditsUsed:              e.CreditsUsed,
		ActualTokensUsed:         actualTokensUsed(e),
		ActualCreditsUsed:        actualCreditsUsed(e),
		RequestsCount:            e.RequestsCount,
		BillingMultiplierEnabled: e.BillingMultiplierEnabled,
		TokenMultiplier:          config.EffectiveTokenMultiplier(e),
		CreditMultiplier:         config.EffectiveCreditMultiplier(e),
		MaxConcurrentIPs:         e.MaxConcurrentIPs,
		MaxTotalIPs:              e.MaxTotalIPs,
		IPAllowlist:              e.IPAllowlist,
		ConcurrentIPs:            conc,
		TotalIPs:                 total,
		RPMLimit:                 e.RPMLimit,
		TPMLimit:                 e.TPMLimit,
	}
}

func (h *Handler) apiListApiKeys(w http.ResponseWriter, r *http.Request) {
	entries := config.ListApiKeys()
	out := make([]apiKeyView, len(entries))
	for i, e := range entries {
		out[i] = toApiKeyView(e)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"apiKeys": out})
}

func (h *Handler) apiGetApiKey(w http.ResponseWriter, r *http.Request, id string) {
	entry := config.GetApiKeyEntry(id)
	if entry == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "API key not found"})
		return
	}
	json.NewEncoder(w).Encode(toApiKeyView(*entry))
}

type apiKeyCreateRequest struct {
	Name                     string  `json:"name,omitempty"`
	Key                      string  `json:"key,omitempty"`
	Enabled                  *bool   `json:"enabled,omitempty"`
	TokenLimit               int64   `json:"tokenLimit,omitempty"`
	CreditLimit              float64 `json:"creditLimit,omitempty"`
	ExpiresAt                int64   `json:"expiresAt,omitempty"`
	RPMLimit                 int     `json:"rpmLimit,omitempty"`
	TPMLimit                 int64   `json:"tpmLimit,omitempty"`
	BillingMultiplierEnabled bool    `json:"billingMultiplierEnabled,omitempty"`
	TokenMultiplier          float64 `json:"tokenMultiplier,omitempty"`
	CreditMultiplier         float64 `json:"creditMultiplier,omitempty"`
}

func (h *Handler) apiCreateApiKey(w http.ResponseWriter, r *http.Request) {
	var req apiKeyCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	entry, err := createApiKeyFromRequest(req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Return the cleartext key exactly once on creation so the operator can copy it.
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"id":      entry.ID,
		"key":     entry.Key,
		"apiKey":  toApiKeyView(entry),
	})
}

func createApiKeyFromRequest(req apiKeyCreateRequest) (config.ApiKeyEntry, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	keyValue := req.Key
	if keyValue == "" {
		keyValue = config.GenerateApiKeyValue()
	}

	return config.AddApiKey(config.ApiKeyEntry{
		Name:                     req.Name,
		Key:                      keyValue,
		Enabled:                  enabled,
		TokenLimit:               req.TokenLimit,
		CreditLimit:              req.CreditLimit,
		ExpiresAt:                req.ExpiresAt,
		RPMLimit:                 req.RPMLimit,
		TPMLimit:                 req.TPMLimit,
		BillingMultiplierEnabled: req.BillingMultiplierEnabled,
		TokenMultiplier:          req.TokenMultiplier,
		CreditMultiplier:         req.CreditMultiplier,
	})
}

type apiKeyBulkCreateRequest struct {
	Count       int     `json:"count"`
	NamePrefix  string  `json:"namePrefix,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
	TokenLimit  int64   `json:"tokenLimit,omitempty"`
	CreditLimit float64 `json:"creditLimit,omitempty"`
	ExpiresAt   int64   `json:"expiresAt,omitempty"`

	MaxConcurrentIPs         int      `json:"maxConcurrentIps,omitempty"`
	MaxTotalIPs              int      `json:"maxTotalIps,omitempty"`
	IPAllowlist              []string `json:"ipAllowlist,omitempty"`
	RPMLimit                 int      `json:"rpmLimit,omitempty"`
	TPMLimit                 int64    `json:"tpmLimit,omitempty"`
	BillingMultiplierEnabled bool     `json:"billingMultiplierEnabled,omitempty"`
	TokenMultiplier          float64  `json:"tokenMultiplier,omitempty"`
	CreditMultiplier         float64  `json:"creditMultiplier,omitempty"`
}

func (h *Handler) apiBulkCreateApiKeys(w http.ResponseWriter, r *http.Request) {
	var req apiKeyBulkCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	if req.Count <= 0 || req.Count > 100 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "count must be between 1 and 100"})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	prefix := req.NamePrefix
	if prefix == "" {
		prefix = "API Key"
	}

	entries := make([]config.ApiKeyEntry, req.Count)
	for i := range entries {
		entries[i] = config.ApiKeyEntry{
			Name:                     prefix + " " + strconv.Itoa(i+1),
			Key:                      config.GenerateApiKeyValue(),
			Enabled:                  enabled,
			TokenLimit:               req.TokenLimit,
			CreditLimit:              req.CreditLimit,
			ExpiresAt:                req.ExpiresAt,
			MaxConcurrentIPs:         req.MaxConcurrentIPs,
			MaxTotalIPs:              req.MaxTotalIPs,
			IPAllowlist:              req.IPAllowlist,
			RPMLimit:                 req.RPMLimit,
			TPMLimit:                 req.TPMLimit,
			BillingMultiplierEnabled: req.BillingMultiplierEnabled,
			TokenMultiplier:          req.TokenMultiplier,
			CreditMultiplier:         req.CreditMultiplier,
		}
	}
	created, err := config.AddApiKeys(entries)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	keys := make([]string, len(created))
	views := make([]apiKeyView, len(created))
	for i, entry := range created {
		keys[i] = entry.Key
		views[i] = toApiKeyView(entry)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"count":   len(created),
		"keys":    keys,
		"apiKeys": views,
	})
}

type apiKeyBulkDeleteRequest struct {
	IDs []string `json:"ids"`
}

func (h *Handler) apiBulkDeleteApiKeys(w http.ResponseWriter, r *http.Request) {
	var req apiKeyBulkDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	deleted, err := config.DeleteApiKeys(req.IDs)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "deleted": deleted})
}

type apiKeyUpdateRequest struct {
	Name        *string  `json:"name,omitempty"`
	Key         *string  `json:"key,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
	TokenLimit  *int64   `json:"tokenLimit,omitempty"`
	CreditLimit *float64 `json:"creditLimit,omitempty"`
	ExpiresAt   *int64   `json:"expiresAt,omitempty"`

	MaxConcurrentIPs *int      `json:"maxConcurrentIps,omitempty"`
	MaxTotalIPs      *int      `json:"maxTotalIps,omitempty"`
	IPAllowlist      *[]string `json:"ipAllowlist,omitempty"`

	RPMLimit                 *int     `json:"rpmLimit,omitempty"`
	TPMLimit                 *int64   `json:"tpmLimit,omitempty"`
	BillingMultiplierEnabled *bool    `json:"billingMultiplierEnabled,omitempty"`
	TokenMultiplier          *float64 `json:"tokenMultiplier,omitempty"`
	CreditMultiplier         *float64 `json:"creditMultiplier,omitempty"`
}

func (h *Handler) apiUpdateApiKey(w http.ResponseWriter, r *http.Request, id string) {
	existing := config.GetApiKeyEntry(id)
	if existing == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "API key not found"})
		return
	}

	var req apiKeyUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	patch := *existing
	if req.Name != nil {
		patch.Name = *req.Name
	}
	if req.Key != nil {
		patch.Key = *req.Key
	}
	if req.Enabled != nil {
		patch.Enabled = *req.Enabled
	}
	if req.TokenLimit != nil {
		patch.TokenLimit = *req.TokenLimit
	}
	if req.CreditLimit != nil {
		patch.CreditLimit = *req.CreditLimit
	}
	if req.ExpiresAt != nil {
		patch.ExpiresAt = *req.ExpiresAt
	}
	if req.MaxConcurrentIPs != nil {
		patch.MaxConcurrentIPs = *req.MaxConcurrentIPs
	}
	if req.MaxTotalIPs != nil {
		patch.MaxTotalIPs = *req.MaxTotalIPs
	}
	if req.IPAllowlist != nil {
		patch.IPAllowlist = *req.IPAllowlist
	}
	if req.RPMLimit != nil {
		patch.RPMLimit = *req.RPMLimit
	}
	if req.TPMLimit != nil {
		patch.TPMLimit = *req.TPMLimit
	}
	if req.BillingMultiplierEnabled != nil {
		patch.BillingMultiplierEnabled = *req.BillingMultiplierEnabled
	}
	if req.TokenMultiplier != nil {
		patch.TokenMultiplier = *req.TokenMultiplier
	}
	if req.CreditMultiplier != nil {
		patch.CreditMultiplier = *req.CreditMultiplier
	}

	if err := config.UpdateApiKey(id, patch); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	updated := config.GetApiKeyEntry(id)
	if updated == nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to reload entry"})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"apiKey":  toApiKeyView(*updated),
	})
}

// apiKeyExportView is the masked usage-report row. It extends the masked fields
// with server-computed derived columns so the frontend renders both JSON and CSV
// from one source. Never contains the raw key value.
type apiKeyExportView struct {
	ID                       string  `json:"id"`
	Name                     string  `json:"name,omitempty"`
	KeyMasked                string  `json:"keyMasked"`
	Key                      string  `json:"key,omitempty"` // Raw value, only populated when export is called with includeSecret=true.
	Enabled                  bool    `json:"enabled"`
	RequestsCount            int64   `json:"requestsCount"`
	TokensUsed               int64   `json:"tokensUsed"`
	CreditsUsed              float64 `json:"creditsUsed"`
	ActualTokensUsed         int64   `json:"actualTokensUsed"`
	ActualCreditsUsed        float64 `json:"actualCreditsUsed"`
	TokenLimit               int64   `json:"tokenLimit"`
	CreditLimit              float64 `json:"creditLimit"`
	BillingMultiplierEnabled bool    `json:"billingMultiplierEnabled"`
	TokenMultiplier          float64 `json:"tokenMultiplier"`
	CreditMultiplier         float64 `json:"creditMultiplier"`
	ExpiresAt                int64   `json:"expiresAt"`
	CreatedAt                int64   `json:"createdAt"`
	LastUsedAt               int64   `json:"lastUsedAt"`
	TokenPercentUsed         float64 `json:"tokenPercentUsed"`
	CreditPercentUsed        float64 `json:"creditPercentUsed"`
	OverToken                bool    `json:"overToken"`
	OverCredit               bool    `json:"overCredit"`
	Expired                  bool    `json:"expired"`
}

func toApiKeyExportView(e config.ApiKeyEntry, includeSecret bool) apiKeyExportView {
	overToken, overCredit := config.ApiKeyOverLimit(e)
	tokenPct := 0.0
	if e.TokenLimit > 0 {
		tokenPct = float64(e.TokensUsed) / float64(e.TokenLimit) * 100
	}
	creditPct := 0.0
	if e.CreditLimit > 0 {
		creditPct = e.CreditsUsed / e.CreditLimit * 100
	}
	rawKey := ""
	if includeSecret {
		rawKey = e.Key
	}
	return apiKeyExportView{
		ID:                       e.ID,
		Name:                     e.Name,
		KeyMasked:                config.MaskApiKey(e.Key),
		Key:                      rawKey,
		Enabled:                  e.Enabled,
		RequestsCount:            e.RequestsCount,
		TokensUsed:               e.TokensUsed,
		CreditsUsed:              e.CreditsUsed,
		ActualTokensUsed:         actualTokensUsed(e),
		ActualCreditsUsed:        actualCreditsUsed(e),
		TokenLimit:               e.TokenLimit,
		CreditLimit:              e.CreditLimit,
		BillingMultiplierEnabled: e.BillingMultiplierEnabled,
		TokenMultiplier:          config.EffectiveTokenMultiplier(e),
		CreditMultiplier:         config.EffectiveCreditMultiplier(e),
		ExpiresAt:                e.ExpiresAt,
		CreatedAt:                e.CreatedAt,
		LastUsedAt:               e.LastUsedAt,
		TokenPercentUsed:         tokenPct,
		CreditPercentUsed:        creditPct,
		OverToken:                overToken,
		OverCredit:               overCredit,
		Expired:                  config.ApiKeyExpired(e),
	}
}

// apiExportApiKeys handles POST /admin/api/api-keys/export.
//
// By default it returns a masked usage report (keys are obscured). When the body
// sets "includeSecret": true, the raw key value is included in the "key" field so
// the export can be re-imported as a backup. Body: {"ids": [...], "includeSecret": bool};
// empty/missing ids = all.
func (h *Handler) apiExportApiKeys(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs           []string `json:"ids"`
		IncludeSecret bool     `json:"includeSecret"`
	}
	// Empty/invalid body = export all, masked.
	_ = json.NewDecoder(r.Body).Decode(&req)

	entries := config.ListApiKeys()
	if len(req.IDs) > 0 {
		idSet := make(map[string]bool, len(req.IDs))
		for _, id := range req.IDs {
			idSet[id] = true
		}
		filtered := entries[:0]
		for _, e := range entries {
			if idSet[e.ID] {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	views := make([]apiKeyExportView, len(entries))
	for i, e := range entries {
		views[i] = toApiKeyExportView(e, req.IncludeSecret)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"version":    config.Version,
		"exportedAt": time.Now().Unix(),
		"apiKeys":    views,
	})
}

// apiKeyImportEntry is one key in an import payload. It mirrors the export
// shape; only Key is required (masked-only exports cannot be re-imported).
type apiKeyImportEntry struct {
	Name                     string  `json:"name,omitempty"`
	Key                      string  `json:"key"`
	Enabled                  *bool   `json:"enabled,omitempty"`
	TokenLimit               int64   `json:"tokenLimit,omitempty"`
	CreditLimit              float64 `json:"creditLimit,omitempty"`
	ExpiresAt                int64   `json:"expiresAt,omitempty"`
	TokensUsed               int64   `json:"tokensUsed,omitempty"`
	CreditsUsed              float64 `json:"creditsUsed,omitempty"`
	ActualTokensUsed         int64   `json:"actualTokensUsed,omitempty"`
	ActualCreditsUsed        float64 `json:"actualCreditsUsed,omitempty"`
	RequestsCount            int64   `json:"requestsCount,omitempty"`
	BillingMultiplierEnabled bool    `json:"billingMultiplierEnabled,omitempty"`
	TokenMultiplier          float64 `json:"tokenMultiplier,omitempty"`
	CreditMultiplier         float64 `json:"creditMultiplier,omitempty"`
}

// apiImportApiKeysAdmin handles POST /admin/api/api-keys/import. It restores keys
// from an export produced with includeSecret=true. Entries whose key is empty or
// masked (contains "***") are skipped, as are keys already present. Name, limits,
// expiry, and usage counters are preserved so the import is a faithful backup.
//
// Body accepts either the full export wrapper {"apiKeys": [...]} or a bare array.
func (h *Handler) apiImportApiKeysAdmin(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to read body"})
		return
	}

	var wrapper struct {
		ApiKeys []apiKeyImportEntry `json:"apiKeys"`
	}
	var items []apiKeyImportEntry
	if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.ApiKeys != nil {
		items = wrapper.ApiKeys
	} else if err := json.Unmarshal(body, &items); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON: expected {\"apiKeys\": [...]} or an array"})
		return
	}

	existing := make(map[string]bool)
	for _, e := range config.ListApiKeys() {
		existing[e.Key] = true
	}

	imported, skipped := 0, 0
	seen := make(map[string]bool)
	for _, it := range items {
		key := strings.TrimSpace(it.Key)
		if key == "" || strings.Contains(key, "***") || strings.Contains(key, "…") {
			skipped++
			continue
		}
		if existing[key] || seen[key] {
			skipped++
			continue
		}
		seen[key] = true

		enabled := true
		if it.Enabled != nil {
			enabled = *it.Enabled
		}
		if _, err := config.AddApiKey(config.ApiKeyEntry{
			Name:                     it.Name,
			Key:                      key,
			Enabled:                  enabled,
			TokenLimit:               it.TokenLimit,
			CreditLimit:              it.CreditLimit,
			ExpiresAt:                it.ExpiresAt,
			TokensUsed:               it.TokensUsed,
			CreditsUsed:              it.CreditsUsed,
			ActualTokensUsed:         it.ActualTokensUsed,
			ActualCreditsUsed:        it.ActualCreditsUsed,
			RequestsCount:            it.RequestsCount,
			BillingMultiplierEnabled: it.BillingMultiplierEnabled,
			TokenMultiplier:          it.TokenMultiplier,
			CreditMultiplier:         it.CreditMultiplier,
		}); err != nil {
			skipped++
			continue
		}
		imported++
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"total":    len(items),
		"imported": imported,
		"skipped":  skipped,
	})
}

func (h *Handler) apiDeleteApiKey(w http.ResponseWriter, r *http.Request, id string) {
	if err := config.DeleteApiKey(id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) apiResetApiKeyUsage(w http.ResponseWriter, r *http.Request, id string) {
	if err := config.ResetApiKeyUsage(id); err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if h.rateLimiter != nil {
		h.rateLimiter.reset(id)
	}
	updated := config.GetApiKeyEntry(id)
	if updated == nil {
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"apiKey":  toApiKeyView(*updated),
	})
}

func (h *Handler) apiResetApiKeyIPs(w http.ResponseWriter, r *http.Request, id string) {
	if err := config.ResetApiKeyIPs(id); err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	updated := config.GetApiKeyEntry(id)
	if updated == nil {
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"apiKey":  toApiKeyView(*updated),
	})
}
