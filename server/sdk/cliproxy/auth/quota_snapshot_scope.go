package auth

import (
	"strings"
	"time"
)

// ModelOnlyLimit reports whether the fresh quota snapshot for authID shows
// that only a model-scoped window matching model is exhausted while every
// all-models routing window still has headroom. An upstream 429 for that
// model is then the scoped window speaking, not the account, so a caller can
// cool down the model instead of the whole credential.
//
// A missing or stale snapshot returns false: the all-models windows might
// have filled since the last poll, and the caller falls back to the header
// classification.
func (s *QuotaSnapshotStore) ModelOnlyLimit(authID, model string, now time.Time) bool {
	if s == nil {
		return false
	}
	snap, ok := s.Get(authID)
	if !ok || snap.Stale(now, defaultResetAwareStaleAfter) {
		return false
	}
	lowerModel := strings.ToLower(model)
	scopedExhausted := false
	for _, window := range snap.Windows {
		if !window.Routing {
			continue
		}
		utilization := effectiveUtilization(window, now)
		if window.ModelScope == "" {
			if utilization >= 100 {
				return false
			}
			continue
		}
		if utilization >= 100 && window.ResetsAt.After(now) && strings.Contains(lowerModel, window.ModelScope) {
			scopedExhausted = true
		}
	}
	return scopedExhausted
}
