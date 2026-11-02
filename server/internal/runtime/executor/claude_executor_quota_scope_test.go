package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// A Fable 429 whose headers do not spell out the shared 5h/7d windows is
// ambiguous on its own and would cool the whole credential until the unified
// reset. When the account's fresh quota snapshot shows only the Fable window
// exhausted, the cooldown must stay on Fable so Opus keeps using the account.
func TestClaudeExecutor_AuthManager_AmbiguousFableRejectionScopedByQuotaSnapshot(t *testing.T) {
	var fableAttempts, opusAttempts atomic.Int32
	now := time.Now()
	reset := now.Add(3*24*time.Hour + 12*time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			http.Error(w, "failed to read sanitized test request", http.StatusBadRequest)
			return
		}
		switch {
		case strings.Contains(string(body), `"model":"claude-fable-5-1"`):
			fableAttempts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Anthropic-Ratelimit-Unified-Status", "rejected")
			w.Header().Set("Anthropic-Ratelimit-Unified-Reset", strconv.FormatInt(reset.Unix(), 10))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit. Please try again later."}}`))
		case strings.Contains(string(body), `"model":"claude-opus-5-5"`):
			opusAttempts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"msg-opus-ok","type":"message","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"ok"}]}`))
		default:
			http.Error(w, "unexpected sanitized test model", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	manager := cliproxyauth.NewManager(nil, nil, nil)
	manager.SetRetryConfig(0, 0, 0)
	manager.RegisterExecutor(NewClaudeExecutor(&config.Config{DisableCooling: false}))

	auth := &cliproxyauth.Auth{
		ID:       uuid.NewString() + "-fable-quota-scope",
		Provider: "claude",
		Attributes: map[string]string{
			"api_key":  "sanitized-test-key",
			"base_url": server.URL,
		},
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "claude", []*registry.ModelInfo{{ID: "claude-fable-5-1"}, {ID: "claude-opus-5-5"}})
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	snapshots := cliproxyauth.DefaultQuotaSnapshots()
	snapshots.Put(cliproxyauth.QuotaSnapshot{
		AuthID:    auth.ID,
		Provider:  "claude",
		FetchedAt: now,
		Windows: []cliproxyauth.QuotaWindow{
			{Key: "seven_day", Label: "7-day", Utilization: 68, ResetsAt: reset, Weekly: true, Routing: true},
			{Key: "iguana_necktie", Label: "7-day Fable", Utilization: 100, ResetsAt: reset, Weekly: true, ModelScope: "fable", Routing: true},
		},
	})
	t.Cleanup(func() { snapshots.Delete(auth.ID) })

	payloadFable := []byte(`{"model":"claude-fable-5-1","messages":[{"role":"user","content":[{"type":"text","text":"test"}]}]}`)
	_, errFable := manager.Execute(context.Background(), []string{"claude"}, cliproxyexecutor.Request{
		Model:   "claude-fable-5-1",
		Payload: payloadFable,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude})
	if errFable == nil {
		t.Fatal("expected Fable request to be rate limited")
	}
	if got := fableAttempts.Load(); got != 1 {
		t.Fatalf("Fable upstream attempts = %d, want 1", got)
	}

	updatedAuth, ok := manager.GetByID(auth.ID)
	if !ok || updatedAuth == nil {
		t.Fatal("auth not found")
	}
	if updatedAuth.Quota.Exceeded && updatedAuth.Quota.Reason == "credential_quota" {
		t.Fatalf("Fable-only rejection cooled the whole credential: %+v", updatedAuth.Quota)
	}
	if updatedAuth.ModelStates["claude-fable-5-1"] == nil {
		t.Fatal("expected a Fable model-state cooldown")
	}

	payloadOpus := []byte(`{"model":"claude-opus-5-5","messages":[{"role":"user","content":[{"type":"text","text":"test"}]}]}`)
	_, errOpus := manager.Execute(context.Background(), []string{"claude"}, cliproxyexecutor.Request{
		Model:   "claude-opus-5-5",
		Payload: payloadOpus,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude})
	if errOpus != nil {
		t.Fatalf("expected Opus to reach upstream on the same credential, got: %v", errOpus)
	}
	if got := opusAttempts.Load(); got != 1 {
		t.Fatalf("Opus upstream attempts = %d, want 1", got)
	}
}
