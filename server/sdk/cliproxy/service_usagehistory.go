package cliproxy

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// syncUsageHistory points the usage history recorder at <auth-dir>/usage
// (or disables it) and resolves auth ids to account emails through the
// core manager. Home mode keeps no local history.
func (s *Service) syncUsageHistory(cfg *config.Config) {
	if s == nil {
		return
	}
	if cfg == nil || cfg.Home.Enabled {
		usagehistory.Configure("")
		return
	}
	if manager := s.coreManager; manager != nil {
		usagehistory.SetAccountResolver(func(authID string) string {
			auth, ok := manager.GetByID(authID)
			if !ok || auth == nil {
				return ""
			}
			if email, _ := auth.Metadata["email"].(string); strings.TrimSpace(email) != "" {
				return strings.TrimSpace(email)
			}
			return strings.TrimSpace(auth.Label)
		})
	}
	usagehistory.Configure(usagehistory.DirFor(cfg))
}
