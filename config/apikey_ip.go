package config

import (
	"errors"
	"net"
	"time"
)

// maxSeenIPsHardLimit bounds SeenIPs when MaxTotalIPs == 0 (unlimited), so the
// list cannot grow without bound. Oldest-LastSeen entries are evicted first.
const maxSeenIPsHardLimit = 1000

// ipMatchesAllowlist reports whether ip matches any entry in list. Each entry is
// parsed as a CIDR when it contains "/", otherwise as an exact IP. Unparseable
// entries are skipped. An empty list returns false (callers treat empty as "no
// allowlist" before calling this).
func ipMatchesAllowlist(ip string, list []string) bool {
	parsed := net.ParseIP(ip)
	for _, raw := range list {
		if raw == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(raw); err == nil {
			if parsed != nil && cidr.Contains(parsed) {
				return true
			}
			continue
		}
		if raw == ip {
			return true
		}
	}
	return false
}

// ApiKeyIPStats returns the concurrent (LastSeen within window) and total distinct
// IP counts for a copied entry. Pure; takes no lock.
func ApiKeyIPStats(e ApiKeyEntry, window time.Duration) (concurrent, total int) {
	total = len(e.SeenIPs)
	cutoff := time.Now().Add(-window).Unix()
	for _, s := range e.SeenIPs {
		if s.LastSeen >= cutoff {
			concurrent++
		}
	}
	return
}

// IPRejectReason names why an IP was rejected. A nil pointer = allowed.
type IPRejectReason string

const (
	IPRejectForbidden    IPRejectReason = "forbidden"               // allowlist miss -> 403
	IPRejectTooManyConc  IPRejectReason = "too_many_concurrent_ips" // -> 429
	IPRejectTooManyTotal IPRejectReason = "too_many_ips"            // -> 429
)

// EnforceAndRecordIP checks the allowlist and both IP caps for keyID against ip,
// records the hit on success, and returns nil when allowed or a non-nil reason
// when rejected. All under one cfgLock acquisition to avoid TOCTOU; successful
// mutations use markDirtyLocked (never a synchronous write on the request hot path).
func EnforceAndRecordIP(keyID, ip string, window time.Duration) *IPRejectReason {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	if cfg == nil {
		return nil
	}
	idx := -1
	for i := range cfg.ApiKeys {
		if cfg.ApiKeys[i].ID == keyID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil // auth already matched; defensive
	}
	e := &cfg.ApiKeys[idx]

	if len(e.IPAllowlist) > 0 && !ipMatchesAllowlist(ip, e.IPAllowlist) {
		r := IPRejectForbidden
		return &r
	}

	now := time.Now().Unix()
	cutoff := now - int64(window.Seconds())
	activeCount := 0
	knownIdx := -1
	for i := range e.SeenIPs {
		if e.SeenIPs[i].LastSeen >= cutoff {
			activeCount++
		}
		if e.SeenIPs[i].IP == ip {
			knownIdx = i
		}
	}

	if knownIdx >= 0 {
		wasActive := e.SeenIPs[knownIdx].LastSeen >= cutoff
		if !wasActive && e.MaxConcurrentIPs > 0 && activeCount >= e.MaxConcurrentIPs {
			r := IPRejectTooManyConc
			return &r
		}
		e.SeenIPs[knownIdx].LastSeen = now
		e.SeenIPs[knownIdx].Count++
		markDirtyLocked()
		return nil
	}

	// New IP.
	if e.MaxTotalIPs > 0 && len(e.SeenIPs) >= e.MaxTotalIPs {
		r := IPRejectTooManyTotal
		return &r
	}
	if e.MaxConcurrentIPs > 0 && activeCount >= e.MaxConcurrentIPs {
		r := IPRejectTooManyConc
		return &r
	}
	e.SeenIPs = append(e.SeenIPs, SeenIP{IP: ip, FirstSeen: now, LastSeen: now, Count: 1})
	if e.MaxTotalIPs == 0 && len(e.SeenIPs) > maxSeenIPsHardLimit {
		evictOldestSeenIP(e)
	}
	markDirtyLocked()
	return nil
}

// evictOldestSeenIP removes the entry with the smallest LastSeen. Caller holds cfgLock.
func evictOldestSeenIP(e *ApiKeyEntry) {
	if len(e.SeenIPs) == 0 {
		return
	}
	oldest := 0
	for i := 1; i < len(e.SeenIPs); i++ {
		if e.SeenIPs[i].LastSeen < e.SeenIPs[oldest].LastSeen {
			oldest = i
		}
	}
	e.SeenIPs = append(e.SeenIPs[:oldest], e.SeenIPs[oldest+1:]...)
}

// ResetApiKeyIPs clears the SeenIPs list for an entry (admin action, not hot path).
func ResetApiKeyIPs(id string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	if cfg == nil {
		return errors.New("config not initialized")
	}
	for i := range cfg.ApiKeys {
		if cfg.ApiKeys[i].ID == id {
			cfg.ApiKeys[i].SeenIPs = nil
			return saveLocked()
		}
	}
	return errors.New("api key not found")
}

// UpdateApiKeySeenIPsForTest overwrites SeenIPs for an entry. Test seam only.
func UpdateApiKeySeenIPsForTest(id string, seen []SeenIP) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i := range cfg.ApiKeys {
		if cfg.ApiKeys[i].ID == id {
			cfg.ApiKeys[i].SeenIPs = seen
			return saveLocked()
		}
	}
	return errors.New("api key not found")
}
