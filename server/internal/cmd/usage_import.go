package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	log "github.com/sirupsen/logrus"
)

// DoImportUsage imports Claude Code transcripts into the usage history,
// attributing each reply to an account from the proxy logs. transcripts is a
// comma-separated list of folders; empty means ~/.claude/projects.
func DoImportUsage(cfg *config.Config, transcripts string) {
	if cfg == nil {
		cfg = &config.Config{}
	}
	authDir, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil {
		log.Errorf("import-usage: resolve auth dir: %v", err)
		return
	}
	usageDir := filepath.Join(authDir, "usage")
	logsDir := filepath.Join(authDir, "logs")
	if _, errStat := os.Stat(logsDir); errStat != nil {
		logsDir = logging.ResolveLogDirectory(cfg)
	}
	var dirs []string
	for _, d := range strings.Split(transcripts, ",") {
		if d = strings.TrimSpace(d); d != "" {
			dirs = append(dirs, d)
		}
	}

	report, err := usagehistory.Import(usagehistory.ImportOptions{
		UsageDir:       usageDir,
		AuthDir:        authDir,
		LogsDir:        logsDir,
		TranscriptDirs: dirs,
	})
	if err != nil {
		log.Errorf("import-usage: %v", err)
		return
	}
	fmt.Printf("Usage import into %s\n", usageDir)
	fmt.Printf("  cutoff (first proxy-recorded request, or now): %s\n", report.Cutoff.Local().Format(time.RFC3339))
	if report.LogsFrom.IsZero() {
		fmt.Printf("  proxy logs: none found in %s\n", logsDir)
	} else {
		fmt.Printf("  proxy logs: %s to %s, %d requests with an account\n",
			report.LogsFrom.Format("2006-01-02 15:04"), report.LogsTo.Format("2006-01-02 15:04"), report.ProxyRequests)
	}
	fmt.Printf("  transcript replies: %d\n", report.Replies)
	fmt.Printf("    matched to one account:        %d\n", report.Matched)
	fmt.Printf("    resolved by session:           %d\n", report.Ambiguous)
	fmt.Printf("    no matching proxy request:     %d (account unknown)\n", report.Unmatched)
	fmt.Printf("    before the proxy logs start:   %d (account unknown)\n", report.BeforeLogs)
	months := make([]string, 0, len(report.Written))
	for m := range report.Written {
		months = append(months, m)
	}
	sort.Strings(months)
	for _, m := range months {
		fmt.Printf("  %s: %d records\n", m, report.Written[m])
	}
}
