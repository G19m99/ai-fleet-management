package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUsageHistoryEnabledDefaultsOnAndCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	cfg, err := LoadConfig(write("config-version: 8\nserver:\n  port: 8317\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UsageHistoryEnabled {
		t.Fatal("usage history should default to enabled")
	}

	cfg, err = LoadConfig(write("config-version: 8\nserver:\n  port: 8317\nobservability:\n  usage:\n    usage-history-enabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UsageHistoryEnabled {
		t.Fatal("observability.usage.usage-history-enabled: false should disable usage history")
	}
}
