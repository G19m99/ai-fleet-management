package usagehistory

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// flushInterval bounds how long a written line may sit in the buffer.
const flushInterval = 2 * time.Second

// Recorder appends proxy usage records to monthly files. The zero value is
// disabled; Configure with a directory to enable it.
type Recorder struct {
	mu      sync.Mutex
	dir     string
	month   string
	file    *os.File
	buf     *bufio.Writer
	dirty   bool
	flusher bool
	// resolve maps an auth id to an account name; nil falls back to the id.
	resolve func(authID string) string
	now     func() time.Time
}

var defaultRecorder = &Recorder{now: time.Now}

func init() {
	coreusage.RegisterNamedPlugin("usage-history", defaultRecorder)
}

// Default returns the process-wide recorder registered as a usage plugin.
func Default() *Recorder { return defaultRecorder }

// Configure sets the usage directory; "" disables recording.
func Configure(dir string) { defaultRecorder.Configure(dir) }

// SetAccountResolver installs the auth-id to account-name lookup.
func SetAccountResolver(fn func(authID string) string) {
	defaultRecorder.mu.Lock()
	defaultRecorder.resolve = fn
	defaultRecorder.mu.Unlock()
}

// Flush writes buffered records to disk.
func Flush() { defaultRecorder.Flush() }

// Dir returns the configured usage directory ("" when disabled).
func (r *Recorder) Dir() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dir
}

// Configure switches the recorder to dir, closing any open file first.
func (r *Recorder) Configure(dir string) {
	dir = strings.TrimSpace(dir)
	r.mu.Lock()
	defer r.mu.Unlock()
	if dir == r.dir {
		return
	}
	r.closeLocked()
	r.dir = dir
	if dir != "" {
		log.Infof("usage history: recording to %s", dir)
		if !r.flusher {
			r.flusher = true
			go r.flushLoop()
		}
	}
}

// HandleUsage implements coreusage.Plugin.
func (r *Recorder) HandleUsage(ctx context.Context, record coreusage.Record) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Token-count probes (generate=false) are not billed.
	if r.dir == "" || !coreusage.GenerateEnabled(record.Generate) {
		return
	}
	rec := r.fromUsage(ctx, record)
	if err := r.appendLocked(rec); err != nil {
		log.WithError(err).Warn("usage history: write failed")
	}
}

func (r *Recorder) fromUsage(ctx context.Context, record coreusage.Record) Record {
	ts := record.RequestedAt
	if ts.IsZero() {
		ts = r.now()
	} else if record.Latency > 0 {
		ts = ts.Add(record.Latency)
	}
	// The canonical breakdown has non-overlapping buckets for every provider
	// (OpenAI-style usage counts cached tokens inside input_tokens; Claude does not).
	detail := coreusage.EnsureTokenBreakdownForProvider(record.Detail, record.Provider, record.ExecutorType)
	in, cacheRead, cacheWrite, out := detail.InputTokens, detail.CacheReadTokens, detail.CacheCreationTokens, detail.OutputTokens
	if b := detail.TokenBreakdown; b.Valid() {
		in, cacheRead, cacheWrite, out = b.Input.UncachedTokens, b.Input.CacheReadTokens, b.Input.CacheWriteTokens, b.Output.TotalTokens
	}
	write1h := detail.CacheCreation1hTokens
	if write1h > cacheWrite {
		write1h = cacheWrite
	}
	account := ""
	if id := strings.TrimSpace(record.AuthID); id != "" {
		if r.resolve != nil {
			account = r.resolve(id)
		}
		if account == "" {
			account = AccountFromAuthID(id)
		}
	}
	model := strings.TrimSpace(record.ResponseModel)
	if model == "" {
		model = strings.TrimSpace(record.Model)
	}
	machine := strings.TrimSpace(internallogging.GetClientRequestMetadata(ctx).ClientIP)
	return Record{
		TS:           ts.UTC(),
		Src:          SrcProxy,
		Provider:     strings.ToLower(strings.TrimSpace(record.Provider)),
		Account:      account,
		Model:        model,
		Machine:      machine,
		In:           in,
		Out:          out,
		CacheRead:    cacheRead,
		CacheWrite5m: cacheWrite - write1h,
		CacheWrite1h: write1h,
		Speed:        strings.TrimSpace(detail.Speed),
		Failed:       record.Failed,
	}
}

func (r *Recorder) appendLocked(rec Record) error {
	month := monthKey(rec.TS)
	if r.file == nil || month != r.month {
		r.closeLocked()
		if err := os.MkdirAll(r.dir, 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(r.dir, proxyFileName(month)), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		r.file, r.month, r.buf = f, month, bufio.NewWriterSize(f, 64*1024)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err = r.buf.Write(line); err != nil {
		return err
	}
	r.dirty = true
	return nil
}

// Flush writes buffered lines to the open file.
func (r *Recorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushLocked()
}

func (r *Recorder) flushLocked() {
	if r.buf == nil || !r.dirty {
		return
	}
	if err := r.buf.Flush(); err != nil {
		log.WithError(err).Warn("usage history: flush failed")
		return
	}
	r.dirty = false
}

func (r *Recorder) closeLocked() {
	r.flushLocked()
	if r.file != nil {
		if err := r.file.Close(); err != nil {
			log.WithError(err).Warn("usage history: close failed")
		}
	}
	r.file, r.buf, r.month, r.dirty = nil, nil, "", false
}

func (r *Recorder) flushLoop() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for range ticker.C {
		r.Flush()
	}
}
