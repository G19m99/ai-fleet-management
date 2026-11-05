package usagehistory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func readRecords(t *testing.T, path string) []Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []Record
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

func TestRecorderWritesClaudeRecordWithCacheSplit(t *testing.T) {
	dir := t.TempDir()
	r := &Recorder{now: time.Now}
	r.resolve = func(id string) string {
		if id == "claude-738c4679-me@example.com.json" {
			return "me@example.com"
		}
		return ""
	}
	r.Configure(dir)
	defer r.Configure("")

	start := time.Date(2026, 10, 31, 23, 59, 50, 0, time.UTC)
	r.HandleUsage(context.Background(), coreusage.Record{
		Provider:    "claude",
		Model:       "claude-opus-5-5",
		AuthID:      "claude-738c4679-me@example.com.json",
		RequestedAt: start,
		Latency:     20 * time.Second, // finishes in November (UTC)
		Detail: coreusage.Detail{
			InputTokens:           10,
			OutputTokens:          500,
			ReasoningTokens:       100,
			CacheReadTokens:       30_000,
			CacheCreationTokens:   4_000,
			CacheCreation1hTokens: 3_000,
			Speed:                 "standard",
			// As the Claude parser builds it: output_tokens already includes thinking.
			TokenBreakdown: coreusage.NewIndependentTokenBreakdown(10, 30_000, 4_000, 400, 100, 34_510),
		},
	})
	r.HandleUsage(context.Background(), coreusage.Record{
		Provider: "claude", Model: "claude-opus-5-5", RequestedAt: start,
		Generate: coreusage.GenerateFlag(false),
		Detail:   coreusage.Detail{InputTokens: 99},
	})
	r.Flush()

	recs := readRecords(t, filepath.Join(dir, "2026-11.jsonl"))
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1 (count_tokens probes are skipped)", len(recs))
	}
	got := recs[0]
	want := Record{
		TS: start.Add(20 * time.Second), Src: SrcProxy, Provider: "claude", Account: "me@example.com",
		Model: "claude-opus-5-5", In: 10, Out: 500, CacheRead: 30_000, CacheWrite5m: 1_000, CacheWrite1h: 3_000, Speed: "standard",
	}
	if !got.TS.Equal(want.TS) {
		t.Fatalf("ts = %v, want %v", got.TS, want.TS)
	}
	got.TS = want.TS
	if got != want {
		t.Fatalf("record = %+v\nwant     %+v", got, want)
	}
}

func TestRecorderOpenAIStyleCachedInputIsNotDoubleCounted(t *testing.T) {
	dir := t.TempDir()
	r := &Recorder{now: time.Now}
	r.Configure(dir)
	defer r.Configure("")
	r.HandleUsage(context.Background(), coreusage.Record{
		Provider: "codex", ExecutorType: "codex", Model: "gpt-5.5-codex", AuthID: "codex-89be1c96-me@example.com-plus.json",
		RequestedAt: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
		Detail:      coreusage.Detail{InputTokens: 1_000, CachedTokens: 800, CacheReadTokens: 800, OutputTokens: 50},
	})
	r.Flush()
	got := readRecords(t, filepath.Join(dir, "2026-11.jsonl"))[0]
	if got.In+got.CacheRead != 1_000 || got.CacheRead != 800 {
		t.Fatalf("in=%d cache_read=%d, want uncached 200 + cached 800", got.In, got.CacheRead)
	}
	if got.Account != "me@example.com-plus" {
		t.Fatalf("account = %q", got.Account)
	}
}

func TestRecorderDisabledWritesNothing(t *testing.T) {
	dir := t.TempDir()
	r := &Recorder{now: time.Now}
	r.HandleUsage(context.Background(), coreusage.Record{Provider: "claude", Model: "claude-opus-5-5"})
	r.Flush()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("disabled recorder wrote %d files", len(entries))
	}
}

func TestAccountFromAuthID(t *testing.T) {
	cases := map[string]string{
		"claude-738c4679-gershy@example.com.json": "gershy@example.com",
		"codex-89be1c96-me@example.com-plus.json": "me@example.com-plus",
		"custom.json": "custom",
	}
	for in, want := range cases {
		if got := AccountFromAuthID(in); got != want {
			t.Fatalf("AccountFromAuthID(%q) = %q, want %q", in, got, want)
		}
	}
}
