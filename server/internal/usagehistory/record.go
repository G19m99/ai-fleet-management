// Package usagehistory keeps a durable, per-request log of token usage so the
// panel can show usage and API-equivalent cost per month, model and account.
//
// Every finished upstream request becomes one JSON line in
// <auth-dir>/usage/YYYY-MM.jsonl (month of the request's UTC timestamp).
// A one-time import of Claude Code transcripts writes the same format to
// YYYY-MM.import.jsonl files, so recorded and imported data stay separate
// and an import can be re-run without duplicating anything.
package usagehistory

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
)

// Source values for Record.Src.
const (
	SrcProxy  = "proxy"
	SrcImport = "import"
)

// Record is one request as stored on disk.
type Record struct {
	// ID is the upstream message id for imported records (merge key); empty for proxy records.
	ID string `json:"id,omitempty"`
	// TS is when the request finished (UTC).
	TS       time.Time `json:"ts"`
	Src      string    `json:"src"`
	Provider string    `json:"provider"`
	// Account is the account's email (or label); empty when unknown.
	Account string `json:"account"`
	Model   string `json:"model"`
	// Machine identifies the client (its IP for proxy records).
	Machine string `json:"machine,omitempty"`
	// In is uncached input tokens; cache reads and writes are separate.
	In           int64 `json:"in"`
	Out          int64 `json:"out"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	// Speed is "fast" for fast-mode requests; empty or "standard" otherwise.
	Speed  string `json:"speed,omitempty"`
	Failed bool   `json:"failed,omitempty"`
}

// PromptTokens is every input token the request was billed for.
func (r Record) PromptTokens() int64 { return r.In + r.CacheRead + r.CacheWrite5m + r.CacheWrite1h }

// DirFor returns the usage directory for cfg: <auth-dir>/usage, or "" when
// usage history is disabled or the auth dir cannot be resolved.
func DirFor(cfg *config.Config) string {
	if cfg == nil || !cfg.UsageHistoryEnabled {
		return ""
	}
	return PathFor(cfg)
}

// PathFor returns <auth-dir>/usage whether or not recording is enabled.
func PathFor(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	authDir, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil || authDir == "" {
		return ""
	}
	return filepath.Join(authDir, "usage")
}

// monthKey is the file month for a timestamp, e.g. "2026-10".
func monthKey(t time.Time) string { return t.UTC().Format("2006-01") }

func proxyFileName(month string) string  { return month + ".jsonl" }
func importFileName(month string) string { return month + ".import.jsonl" }

var usageFileRE = regexp.MustCompile(`^(\d{4}-\d{2})(\.import)?\.jsonl$`)

// parseUsageFileName reports the month and source of a usage file name.
func parseUsageFileName(name string) (month, src string, ok bool) {
	m := usageFileRE.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	if m[2] != "" {
		return m[1], SrcImport, true
	}
	return m[1], SrcProxy, true
}

var authIDRE = regexp.MustCompile(`^[a-z0-9]+-[0-9a-f]{8}-(.+?)\.json$`)

// AccountFromAuthID derives a readable account name from an auth file id
// such as "claude-738c4679-me@example.com.json" when nothing better is known.
func AccountFromAuthID(id string) string {
	id = strings.TrimSpace(filepath.Base(strings.ReplaceAll(id, "\\", "/")))
	if m := authIDRE.FindStringSubmatch(id); m != nil {
		return m[1]
	}
	return strings.TrimSuffix(id, ".json")
}

func monthsBetween(from, to time.Time) []string {
	if !to.After(from) {
		return nil
	}
	var out []string
	cur := time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	last := to.UTC().Add(-time.Nanosecond)
	for !cur.After(last) {
		out = append(out, cur.Format("2006-01"))
		cur = cur.AddDate(0, 1, 0)
	}
	return out
}

func validMonth(month string) error {
	if _, err := time.Parse("2006-01", month); err != nil {
		return fmt.Errorf("invalid month %q", month)
	}
	return nil
}
