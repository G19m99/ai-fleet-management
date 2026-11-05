package usagehistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func appendRecords(t *testing.T, path string, recs ...Record) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueryBucketsDaysInZoneAndSeparatesSources(t *testing.T) {
	dir := t.TempDir()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-01 02:30 UTC is still Oct 31 in New York.
	late := time.Date(2026, 11, 1, 2, 30, 0, 0, time.UTC)
	appendRecords(t, filepath.Join(dir, "2026-11.jsonl"),
		Record{TS: late, Src: SrcProxy, Provider: "claude", Account: "a@x", Model: "claude-opus-5-5", Out: 1_000_000},
		Record{TS: late.Add(10 * time.Hour), Src: SrcProxy, Provider: "claude", Account: "b@x", Model: "claude-opus-5-5", Out: 1_000_000, Failed: true},
	)
	appendRecords(t, filepath.Join(dir, "2026-10.import.jsonl"),
		Record{ID: "msg_1", TS: time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC), Src: SrcImport, Provider: "claude", Model: "claude-fable-5-1-20261001", In: 1_000_000},
	)
	store := &Store{}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, ny)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	res, err := store.Query(dir, from, to, ny)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Months) != 2 || res.Months[0] != "2026-10" || res.Months[1] != "2026-11" {
		t.Fatalf("months = %v", res.Months)
	}
	if res.ProxySince == nil || !res.ProxySince.Equal(late) {
		t.Fatalf("proxy_since = %v, want %v", res.ProxySince, late)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %+v, want the Oct 20 import and the Oct 31 (NY) proxy row", res.Rows)
	}
	imp, proxy := res.Rows[0], res.Rows[1]
	if imp.Day != "2026-10-20" || imp.Src != SrcImport || imp.Model != "claude-fable-5-1" || imp.Account != "" {
		t.Fatalf("import row = %+v", imp)
	}
	approx(t, imp.CostUSD, 10)
	if proxy.Day != "2026-10-31" || proxy.Account != "a@x" || proxy.Requests != 1 {
		t.Fatalf("proxy row = %+v", proxy)
	}
	approx(t, proxy.CostUSD, 20)

	// November in New York picks up the failed request only.
	res, err = store.Query(dir, to, to.AddDate(0, 1, 0), ny)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Failed != 1 || res.Rows[0].Account != "b@x" {
		t.Fatalf("november rows = %+v", res.Rows)
	}
}

func TestQueryReadsAppendedLinesIncrementally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-11.jsonl")
	ts := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	appendRecords(t, path, Record{TS: ts, Src: SrcProxy, Provider: "claude", Account: "a@x", Model: "claude-sonnet-5-5", Out: 10})
	store := &Store{}
	from, to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if res, _ := store.Query(dir, from, to, time.UTC); len(res.Rows) != 1 || res.Rows[0].Requests != 1 {
		t.Fatalf("first read = %+v", res.Rows)
	}
	// A partial trailing line (writer mid-flush) must wait for its newline.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	line, _ := json.Marshal(Record{TS: ts.Add(time.Minute), Src: SrcProxy, Provider: "claude", Account: "a@x", Model: "claude-sonnet-5-5", Out: 5})
	_, _ = f.Write(line[:10])
	_ = f.Close()
	if res, _ := store.Query(dir, from, to, time.UTC); res.Rows[0].Requests != 1 {
		t.Fatalf("partial line was counted: %+v", res.Rows)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.Write(append(line[10:], '\n'))
	_ = f.Close()
	res, _ := store.Query(dir, from, to, time.UTC)
	if res.Rows[0].Requests != 2 || res.Rows[0].Out != 15 {
		t.Fatalf("after append = %+v", res.Rows)
	}
}

func TestQueryMissingDirIsEmpty(t *testing.T) {
	res, err := (&Store{}).Query(filepath.Join(t.TempDir(), "none"), time.Now().Add(-time.Hour), time.Now(), time.UTC)
	if err != nil || len(res.Rows) != 0 || res.Months == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
