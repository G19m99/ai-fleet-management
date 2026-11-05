package usagehistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Matching window between a transcript reply's timestamp and the proxy log's
// request end time. Measured offsets are a few seconds; the window is generous
// and the nearest candidate wins.
const (
	matchBefore = 15 * time.Second
	matchAfter  = 45 * time.Second
)

// ImportOptions configures a transcript import.
type ImportOptions struct {
	// UsageDir receives YYYY-MM.import.jsonl files.
	UsageDir string
	// AuthDir holds the auth files, used to turn auth file names into emails.
	AuthDir string
	// LogsDir holds the proxy's main*.log files.
	LogsDir string
	// TranscriptDirs are Claude Code project folders (default ~/.claude/projects).
	TranscriptDirs []string
	// Until is the exclusive cutoff. Zero means the first proxy-recorded
	// request in UsageDir, or now when there is none.
	Until time.Time
	// LogLocation is the zone of the log timestamps (default time.Local).
	LogLocation *time.Location
	// Machine labels the imported records (default: this host's name).
	Machine string
}

// ImportReport summarizes an import.
type ImportReport struct {
	Cutoff        time.Time
	LogsFrom      time.Time
	LogsTo        time.Time
	ProxyRequests int
	Replies       int
	Matched       int
	Ambiguous     int
	Unmatched     int
	BeforeLogs    int
	Written       map[string]int
}

type logRequest struct {
	end     time.Time
	account string
	model   string
}

type transcriptReply struct {
	id      string
	ts      time.Time
	session string
	model   string
	rec     Record
}

// Import reads Claude Code transcripts and attributes each reply to the
// account the proxy logs show served it, then merges the results into the
// month import files. Re-running is safe: records are merged by reply id.
func Import(opts ImportOptions) (ImportReport, error) {
	report := ImportReport{Written: map[string]int{}}
	if strings.TrimSpace(opts.UsageDir) == "" {
		return report, errors.New("usage dir is required")
	}
	if opts.LogLocation == nil {
		opts.LogLocation = time.Local
	}
	if opts.Machine == "" {
		opts.Machine, _ = os.Hostname()
	}
	if len(opts.TranscriptDirs) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return report, fmt.Errorf("resolve home: %w", err)
		}
		opts.TranscriptDirs = []string{filepath.Join(home, ".claude", "projects")}
	}
	report.Cutoff = opts.Until
	if report.Cutoff.IsZero() {
		report.Cutoff = firstProxyRecord(opts.UsageDir)
	}
	if report.Cutoff.IsZero() {
		report.Cutoff = time.Now().UTC()
	}

	accounts := authEmails(opts.AuthDir)
	requests, from, to, err := parseProxyLogs(opts.LogsDir, opts.LogLocation, accounts)
	if err != nil {
		return report, err
	}
	report.LogsFrom, report.LogsTo = from, to
	for _, list := range requests {
		report.ProxyRequests += len(list)
	}

	replies, err := parseTranscripts(opts.TranscriptDirs)
	if err != nil {
		return report, err
	}
	kept := replies[:0]
	for _, r := range replies {
		if r.ts.Before(report.Cutoff) {
			kept = append(kept, r)
		}
	}
	replies = kept
	report.Replies = len(replies)

	attribute(replies, requests, from, &report)

	byMonth := map[string][]Record{}
	for _, r := range replies {
		r.rec.Machine = opts.Machine
		byMonth[monthKey(r.rec.TS)] = append(byMonth[monthKey(r.rec.TS)], r.rec)
	}
	for month, recs := range byMonth {
		n, errWrite := mergeImportFile(filepath.Join(opts.UsageDir, importFileName(month)), recs)
		if errWrite != nil {
			return report, errWrite
		}
		report.Written[month] = n
	}
	return report, nil
}

// attribute sets each reply's account from the log request that ended
// closest to it. When candidates in the window disagree, the account the
// same session used nearest in time wins.
func attribute(replies []transcriptReply, requests map[string][]logRequest, logsFrom time.Time, report *ImportReport) {
	type pending struct {
		idx        int
		candidates map[string]bool
		nearest    string
	}
	var ambiguous []pending
	sessionHits := map[string][]int{}
	for i := range replies {
		r := &replies[i]
		if logsFrom.IsZero() || r.ts.Before(logsFrom) {
			report.BeforeLogs++
			continue
		}
		list := requests[NormalizeModel(r.model)]
		lo := sort.Search(len(list), func(k int) bool { return !list[k].end.Before(r.ts.Add(-matchBefore)) })
		candidates := map[string]bool{}
		nearest, best := "", time.Duration(1<<62)
		for k := lo; k < len(list) && !list[k].end.After(r.ts.Add(matchAfter)); k++ {
			candidates[list[k].account] = true
			if d := absDuration(list[k].end.Sub(r.ts)); d < best {
				best, nearest = d, list[k].account
			}
		}
		switch len(candidates) {
		case 0:
			report.Unmatched++
		case 1:
			r.rec.Account = nearest
			report.Matched++
			sessionHits[r.session] = append(sessionHits[r.session], i)
		default:
			ambiguous = append(ambiguous, pending{idx: i, candidates: candidates, nearest: nearest})
		}
	}
	for _, p := range ambiguous {
		r := &replies[p.idx]
		account, best := p.nearest, time.Duration(1<<62)
		for _, j := range sessionHits[r.session] {
			other := replies[j]
			if !p.candidates[other.rec.Account] {
				continue
			}
			if d := absDuration(other.ts.Sub(r.ts)); d < best {
				best, account = d, other.rec.Account
			}
		}
		r.rec.Account = account
		report.Ambiguous++
	}
}

var (
	logLineRE  = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\] \[([^\]]+)\] \[[a-z]+ *\] \[([a-z_]+)\.go:\d+\] (.*)$`)
	selectorRE = regexp.MustCompile(`auth=(\S+) provider=\S+ model=(\S+)`)
	ginRE      = regexp.MustCompile(`^(\d{3}) \|\s*\S+ \|\s*\S+ \| POST\s+"/v1/messages`)
)

const logTimeLayout = "2006-01-02 15:04:05"

// parseProxyLogs joins each request's account (selector line) with its end
// time (gin access line) by request id. Results are per model, by end time.
func parseProxyLogs(dir string, loc *time.Location, emails map[string]string) (map[string][]logRequest, time.Time, time.Time, error) {
	out := map[string][]logRequest{}
	var from, to time.Time
	if strings.TrimSpace(dir) == "" {
		return out, from, to, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, from, to, nil
		}
		return nil, from, to, err
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "main") && strings.HasSuffix(name, ".log") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	type started struct{ account, model string }
	open := map[string]started{}
	for _, path := range files {
		errFile := scanLines(path, func(line []byte) {
			m := logLineRE.FindSubmatch(bytes.TrimRight(line, "\r\n"))
			if m == nil {
				return
			}
			ts, errTime := time.ParseInLocation(logTimeLayout, string(m[1]), loc)
			if errTime != nil {
				return
			}
			if from.IsZero() || ts.Before(from) {
				from = ts
			}
			if ts.After(to) {
				to = ts
			}
			id, source, msg := string(m[2]), string(m[3]), m[4]
			switch source {
			case "selector":
				if s := selectorRE.FindSubmatch(msg); s != nil {
					authFile := string(s[1])
					account := emails[authFile]
					if account == "" {
						account = AccountFromAuthID(authFile)
					}
					open[id] = started{account: account, model: NormalizeModel(string(s[2]))}
				}
			case "gin_logger":
				g := ginRE.FindSubmatch(msg)
				if g == nil {
					return
				}
				st, ok := open[id]
				if !ok {
					return
				}
				delete(open, id)
				if status := string(g[1]); status < "200" || status > "299" {
					return
				}
				out[st.model] = append(out[st.model], logRequest{end: ts, account: st.account, model: st.model})
			}
		})
		if errFile != nil {
			return nil, from, to, errFile
		}
	}
	for model := range out {
		list := out[model]
		sort.Slice(list, func(i, j int) bool { return list[i].end.Before(list[j].end) })
	}
	return out, from, to, nil
}

type transcriptLine struct {
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	Message   struct {
		ID    string `json:"id"`
		Role  string `json:"role"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64  `json:"input_tokens"`
			OutputTokens             int64  `json:"output_tokens"`
			CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
			Speed                    string `json:"speed"`
			CacheCreation            *struct {
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// parseTranscripts returns one entry per assistant reply. Claude Code writes
// a reply as several lines (one per content block) sharing a message id; the
// last line carries the final output count, so counts take the maximum.
func parseTranscripts(dirs []string) ([]transcriptReply, error) {
	byID := map[string]*transcriptReply{}
	for _, dir := range dirs {
		errWalk := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			return scanLines(path, func(line []byte) {
				if !bytes.Contains(line, []byte(`"usage"`)) || !bytes.Contains(line, []byte(`"assistant"`)) {
					return
				}
				var tl transcriptLine
				if json.Unmarshal(line, &tl) != nil {
					return
				}
				msg := tl.Message
				if msg.Role != "assistant" || msg.Usage == nil || msg.ID == "" || strings.HasPrefix(msg.Model, "<") {
					return
				}
				ts, errTime := time.Parse(time.RFC3339Nano, tl.Timestamp)
				if errTime != nil {
					return
				}
				u := msg.Usage
				write1h := int64(0)
				if u.CacheCreation != nil {
					write1h = u.CacheCreation.Ephemeral1h
				}
				if write1h > u.CacheCreationInputTokens {
					write1h = u.CacheCreationInputTokens
				}
				cur := byID[msg.ID]
				if cur == nil {
					cur = &transcriptReply{id: msg.ID, ts: ts, session: tl.SessionID, model: msg.Model}
					byID[msg.ID] = cur
				}
				if ts.After(cur.ts) {
					cur.ts = ts
				}
				r := &cur.rec
				r.In = max(r.In, u.InputTokens)
				r.Out = max(r.Out, u.OutputTokens)
				r.CacheRead = max(r.CacheRead, u.CacheReadInputTokens)
				r.CacheWrite1h = max(r.CacheWrite1h, write1h)
				r.CacheWrite5m = max(r.CacheWrite5m, u.CacheCreationInputTokens-write1h)
				if u.Speed != "" {
					r.Speed = u.Speed
				}
			})
		})
		if errWalk != nil {
			return nil, errWalk
		}
	}
	out := make([]transcriptReply, 0, len(byID))
	for _, r := range byID {
		r.rec.ID = r.id
		r.rec.TS = r.ts.UTC()
		r.rec.Src = SrcImport
		r.rec.Provider = "claude"
		r.rec.Model = r.model
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ts.Before(out[j].ts) })
	return out, nil
}

// mergeImportFile unions recs into path by record id (new values win) and
// rewrites it atomically. It returns the number of records in the file.
func mergeImportFile(path string, recs []Record) (int, error) {
	merged := map[string]Record{}
	var order []string
	add := func(r Record) {
		if _, seen := merged[r.ID]; !seen {
			order = append(order, r.ID)
		}
		merged[r.ID] = r
	}
	errRead := scanLines(path, func(line []byte) {
		var r Record
		if json.Unmarshal(bytes.TrimSpace(line), &r) == nil && r.ID != "" {
			add(r)
		}
	})
	if errRead != nil && !errors.Is(errRead, os.ErrNotExist) {
		return 0, errRead
	}
	for _, r := range recs {
		add(r)
	}
	all := make([]Record, 0, len(order))
	for _, id := range order {
		all = append(all, merged[id])
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].TS.Before(all[j].TS) })

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	w := bufio.NewWriterSize(f, 256*1024)
	enc := json.NewEncoder(w)
	for _, r := range all {
		if err = enc.Encode(r); err != nil {
			_ = f.Close()
			return 0, err
		}
	}
	if err = w.Flush(); err != nil {
		_ = f.Close()
		return 0, err
	}
	if err = f.Close(); err != nil {
		return 0, err
	}
	if err = os.Rename(tmp, path); err != nil {
		return 0, err
	}
	return len(all), nil
}

// authEmails maps auth file names to the email stored inside them.
func authEmails(dir string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(dir) == "" {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, errRead := os.ReadFile(filepath.Join(dir, e.Name()))
		if errRead != nil {
			continue
		}
		var meta struct {
			Email string `json:"email"`
		}
		if json.Unmarshal(data, &meta) == nil && strings.TrimSpace(meta.Email) != "" {
			out[e.Name()] = strings.TrimSpace(meta.Email)
		}
	}
	return out
}

// firstProxyRecord is the timestamp of the earliest proxy-recorded request.
func firstProxyRecord(dir string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var names []string
	for _, e := range entries {
		if _, src, ok := parseUsageFileName(e.Name()); ok && src == SrcProxy {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if ts, ok := firstRecordTime(filepath.Join(dir, name)); ok {
			return ts
		}
	}
	return time.Time{}
}

func scanLines(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, errRead := reader.ReadBytes('\n')
		if len(line) > 0 {
			fn(line)
		}
		if errRead != nil {
			if errors.Is(errRead, io.EOF) {
				return nil
			}
			return errRead
		}
	}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
