package usagehistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	// Embed the zone database so day boundaries work on hosts without one (Windows).
	_ "time/tzdata"
)

// bucketWidth is the time granularity cached per file. 15 minutes keeps day
// boundaries exact for every real-world UTC offset.
const bucketWidth = 15 * time.Minute

// Totals are summed token counts and cost.
type Totals struct {
	Requests     int64   `json:"requests"`
	Failed       int64   `json:"failed"`
	In           int64   `json:"in"`
	Out          int64   `json:"out"`
	CacheRead    int64   `json:"cache_read"`
	CacheWrite5m int64   `json:"cache_write_5m"`
	CacheWrite1h int64   `json:"cache_write_1h"`
	CostUSD      float64 `json:"cost_usd"`
	// Unpriced counts requests whose model has no price (cost excludes them).
	Unpriced int64 `json:"unpriced"`
}

func (t *Totals) add(o Totals) {
	t.Requests += o.Requests
	t.Failed += o.Failed
	t.In += o.In
	t.Out += o.Out
	t.CacheRead += o.CacheRead
	t.CacheWrite5m += o.CacheWrite5m
	t.CacheWrite1h += o.CacheWrite1h
	t.CostUSD += o.CostUSD
	t.Unpriced += o.Unpriced
}

func totalsOf(r Record) Totals {
	t := Totals{Requests: 1, In: r.In, Out: r.Out, CacheRead: r.CacheRead, CacheWrite5m: r.CacheWrite5m, CacheWrite1h: r.CacheWrite1h}
	if r.Failed {
		t.Failed = 1
	}
	if usd, ok := Cost(r); ok {
		t.CostUSD = usd
	} else if r.In+r.Out+r.CacheRead+r.CacheWrite5m+r.CacheWrite1h > 0 {
		t.Unpriced = 1
	}
	return t
}

// Row is one day x source x provider x account x model aggregate.
type Row struct {
	Day      string `json:"day"`
	Src      string `json:"src"`
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Model    string `json:"model"`
	Totals
}

// Result is the answer to a usage history query.
type Result struct {
	From string `json:"from"`
	To   string `json:"to"`
	TZ   string `json:"tz"`
	// Months lists every month that has a usage file, oldest first.
	Months []string `json:"months"`
	// ProxySince is the first proxy-recorded request, if any.
	ProxySince *time.Time `json:"proxy_since,omitempty"`
	Rows       []Row      `json:"rows"`
}

type groupKey struct {
	src, provider, account, model string
}

type bucketKey struct {
	slot int64 // unix seconds / bucketWidth
	groupKey
}

type fileCache struct {
	consumed int64
	size     int64
	mod      time.Time
	buckets  map[bucketKey]*Totals
}

// Store reads usage files and caches their parsed buckets.
type Store struct {
	mu    sync.Mutex
	files map[string]*fileCache
}

var defaultStore = &Store{}

// Query aggregates dir's usage between from (inclusive) and to (exclusive),
// bucketed into days in loc.
func Query(dir string, from, to time.Time, loc *time.Location) (Result, error) {
	return defaultStore.Query(dir, from, to, loc)
}

// Query aggregates usage; see the package-level Query.
func (s *Store) Query(dir string, from, to time.Time, loc *time.Location) (Result, error) {
	if loc == nil {
		loc = time.Local
	}
	res := Result{
		From: from.In(loc).Format("2006-01-02"),
		To:   to.In(loc).Format("2006-01-02"),
		TZ:   loc.String(),
		Rows: []Row{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			res.Months = []string{}
			return res, nil
		}
		return res, err
	}
	monthSet := map[string]bool{}
	byMonth := map[string][]string{}
	var proxyFiles []string
	for _, e := range entries {
		month, src, ok := parseUsageFileName(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		monthSet[month] = true
		byMonth[month] = append(byMonth[month], e.Name())
		if src == SrcProxy {
			proxyFiles = append(proxyFiles, e.Name())
		}
	}
	res.Months = sortedKeys(monthSet)
	sort.Strings(proxyFiles)
	if len(proxyFiles) > 0 {
		if ts, ok := firstRecordTime(filepath.Join(dir, proxyFiles[0])); ok {
			res.ProxySince = &ts
		}
	}

	fromSlot, toSlot := from.Unix()/int64(bucketWidth/time.Second), to.Unix()/int64(bucketWidth/time.Second)
	rows := map[string]*Row{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, month := range monthsBetween(from, to) {
		for _, name := range byMonth[month] {
			_, src, _ := parseUsageFileName(name)
			fc, errLoad := s.load(filepath.Join(dir, name), src == SrcProxy)
			if errLoad != nil {
				return res, errLoad
			}
			for key, t := range fc.buckets {
				if key.slot < fromSlot || key.slot >= toSlot {
					continue
				}
				day := time.Unix(key.slot*int64(bucketWidth/time.Second), 0).In(loc).Format("2006-01-02")
				id := day + "\x00" + key.src + "\x00" + key.provider + "\x00" + key.account + "\x00" + key.model
				row := rows[id]
				if row == nil {
					row = &Row{Day: day, Src: key.src, Provider: key.provider, Account: key.account, Model: key.model}
					rows[id] = row
				}
				row.add(*t)
			}
		}
	}
	for _, row := range rows {
		res.Rows = append(res.Rows, *row)
	}
	sort.Slice(res.Rows, func(i, j int) bool {
		a, b := res.Rows[i], res.Rows[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Account != b.Account {
			return a.Account < b.Account
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Src < b.Src
	})
	return res, nil
}

// load refreshes the cache for path. Append-only files are read
// incrementally; others are re-read whenever their size or time changes.
func (s *Store) load(path string, appendOnly bool) (*fileCache, error) {
	if s.files == nil {
		s.files = map[string]*fileCache{}
	}
	st, err := os.Stat(path)
	if err != nil {
		delete(s.files, path)
		if errors.Is(err, os.ErrNotExist) {
			return &fileCache{buckets: map[bucketKey]*Totals{}}, nil
		}
		return nil, err
	}
	fc := s.files[path]
	if fc != nil && fc.size == st.Size() && fc.mod.Equal(st.ModTime()) {
		return fc, nil
	}
	if fc == nil || !appendOnly || st.Size() < fc.consumed {
		fc = &fileCache{buckets: map[bucketKey]*Totals{}}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Seek(fc.consumed, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(f, 256*1024)
	for {
		line, errRead := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			fc.consumed += int64(len(line))
			fc.addLine(line)
		}
		if errRead != nil {
			if errors.Is(errRead, io.EOF) {
				break
			}
			return nil, errRead
		}
	}
	fc.size, fc.mod = st.Size(), st.ModTime()
	s.files[path] = fc
	return fc, nil
}

func (fc *fileCache) addLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var r Record
	if json.Unmarshal(line, &r) != nil || r.TS.IsZero() {
		return
	}
	if r.Src == "" {
		r.Src = SrcProxy
	}
	key := bucketKey{
		slot:     r.TS.Unix() / int64(bucketWidth/time.Second),
		groupKey: groupKey{src: r.Src, provider: r.Provider, account: r.Account, model: NormalizeModel(r.Model)},
	}
	t := fc.buckets[key]
	if t == nil {
		t = &Totals{}
		fc.buckets[key] = t
	}
	t.add(totalsOf(r))
}

func firstRecordTime(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var r Record
		if json.Unmarshal(scanner.Bytes(), &r) == nil && !r.TS.IsZero() {
			return r.TS, true
		}
	}
	return time.Time{}, false
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
