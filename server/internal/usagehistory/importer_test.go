package usagehistory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func transcriptLineJSON(id, session, model, ts string, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":%q,"message":{"id":%q,"role":"assistant","model":%q,`+
		`"usage":{"input_tokens":3,"output_tokens":%d,"cache_read_input_tokens":1000,"cache_creation_input_tokens":500,`+
		`"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":500},"speed":"standard"}}}`,
		ts, session, id, model, out)
}

func writeFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportAttributesRepliesToAccounts(t *testing.T) {
	root := t.TempDir()
	authDir := filepath.Join(root, "auths")
	usageDir := filepath.Join(authDir, "usage")
	logsDir := filepath.Join(authDir, "logs")
	projects := filepath.Join(root, "projects")
	writeFile(t, filepath.Join(authDir, "claude-11111111-a@x.json"), `{"email":"a@x","type":"claude"}`)

	writeFile(t, filepath.Join(logsDir, "main.log"),
		`[2026-10-21 14:59:04] [--------] [info ] [clients.go:195] full client load complete`,
		`[2026-10-21 15:00:00] [aaaa0001] [info ] [selector.go:1218] session-affinity: cache hit | session=claude:6... auth=claude-11111111-a@x.json provider=mixed model=claude-opus-5-5`,
		`[2026-10-21 15:00:05] [aaaa0001] [info ] [gin_logger.go:113] 200 |        5.0s |   100.84.167.41 | POST    "/v1/messages?beta=true"`,
		`[2026-10-21 15:29:50] [aaaa0002] [info ] [selector.go:1218] session-affinity: cache hit | session=claude:6... auth=claude-11111111-a@x.json provider=mixed model=claude-opus-5-5`,
		`[2026-10-21 15:29:55] [aaaa0003] [info ] [selector.go:1288] session-affinity: cache miss, new binding | session=claude:e... auth=claude-22222222-b@x.json provider=mixed model=claude-opus-5-5`,
		`[2026-10-21 15:30:00] [aaaa0002] [info ] [gin_logger.go:113] 200 |       10.0s |   100.84.167.41 | POST    "/v1/messages?beta=true"`,
		`[2026-10-21 15:30:02] [aaaa0003] [info ] [gin_logger.go:113] 200 |        7.0s |   100.84.167.41 | POST    "/v1/messages?beta=true"`,
		`[2026-10-21 15:40:00] [aaaa0004] [info ] [selector.go:1218] session-affinity: cache hit | session=claude:6... auth=claude-22222222-b@x.json provider=mixed model=claude-opus-5-5`,
		`[2026-10-21 15:40:03] [aaaa0004] [info ] [gin_logger.go:113] 429 |        3.0s |   100.84.167.41 | POST    "/v1/messages?beta=true"`,
	)
	writeFile(t, filepath.Join(projects, "proj", "s1.jsonl"),
		transcriptLineJSON("msg_pre", "s1", "claude-opus-5-5", "2026-10-20T10:00:00Z", 10),
		transcriptLineJSON("msg_1", "s1", "claude-opus-5-5", "2026-10-21T15:00:04Z", 2),
		transcriptLineJSON("msg_1", "s1", "claude-opus-5-5", "2026-10-21T15:00:05.200Z", 700),
		// Ambiguous: a ended 15:30:00, b ended 15:30:02; b is nearer but the session used a.
		transcriptLineJSON("msg_2", "s1", "claude-opus-5-5", "2026-10-21T15:30:01.600Z", 50),
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		transcriptLineJSON("msg_syn", "s1", "<synthetic>", "2026-10-21T15:31:00Z", 0),
		transcriptLineJSON("msg_after", "s1", "claude-opus-5-5", "2026-11-02T10:00:00Z", 5),
	)
	writeFile(t, filepath.Join(projects, "proj", "s2", "subagents", "agent.jsonl"),
		transcriptLineJSON("msg_3", "s2", "claude-sonnet-5-5", "2026-10-21T16:00:00Z", 9),
	)

	opts := ImportOptions{
		UsageDir: usageDir, AuthDir: authDir, LogsDir: logsDir,
		TranscriptDirs: []string{projects},
		Until:          time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		LogLocation:    time.UTC,
		Machine:        "pc",
	}
	report, err := Import(opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Replies != 4 || report.Matched != 1 || report.Ambiguous != 1 || report.Unmatched != 1 || report.BeforeLogs != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.ProxyRequests != 3 {
		t.Fatalf("proxy requests = %d, want 3 (the 429 is not a candidate)", report.ProxyRequests)
	}

	recs := readRecords(t, filepath.Join(usageDir, "2026-10.import.jsonl"))
	byID := map[string]Record{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	if len(byID) != 4 {
		t.Fatalf("records = %+v", recs)
	}
	checks := map[string]string{"msg_pre": "", "msg_1": "a@x", "msg_2": "a@x", "msg_3": ""}
	for id, account := range checks {
		if byID[id].Account != account {
			t.Fatalf("%s account = %q, want %q", id, byID[id].Account, account)
		}
	}
	m1 := byID["msg_1"]
	if m1.Out != 700 || m1.In != 3 || m1.CacheRead != 1000 || m1.CacheWrite1h != 500 || m1.CacheWrite5m != 0 || m1.Src != SrcImport || m1.Machine != "pc" {
		t.Fatalf("msg_1 = %+v", m1)
	}

	// Re-running merges by id instead of duplicating.
	if _, err = Import(opts); err != nil {
		t.Fatal(err)
	}
	if n := len(readRecords(t, filepath.Join(usageDir, "2026-10.import.jsonl"))); n != 4 {
		t.Fatalf("after re-import: %d records, want 4", n)
	}
}

func TestImportDefaultCutoffIsFirstProxyRecord(t *testing.T) {
	root := t.TempDir()
	usageDir := filepath.Join(root, "usage")
	first := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(usageDir, "2026-10.jsonl"),
		fmt.Sprintf(`{"ts":%q,"src":"proxy","provider":"claude","account":"a@x","model":"claude-opus-5-5","in":1,"out":1,"cache_read":0,"cache_write_5m":0,"cache_write_1h":0}`, first.Format(time.RFC3339)))
	projects := filepath.Join(root, "projects")
	writeFile(t, filepath.Join(projects, "p", "s.jsonl"),
		transcriptLineJSON("msg_before", "s", "claude-opus-5-5", "2026-10-25T11:59:59Z", 1),
		transcriptLineJSON("msg_after", "s", "claude-opus-5-5", "2026-10-25T12:00:01Z", 1),
	)
	report, err := Import(ImportOptions{UsageDir: usageDir, TranscriptDirs: []string{projects}, LogLocation: time.UTC, Machine: "pc"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Cutoff.Equal(first) || report.Replies != 1 {
		t.Fatalf("report = %+v", report)
	}
}
