package auth

import (
	"testing"
	"time"
)

func TestQuotaSnapshotStore_ModelOnlyLimit(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 15, 0, 0, time.UTC)
	thursday := now.Add(3*24*time.Hour + 12*time.Hour)
	fresh := now.Add(-2 * time.Minute)

	sevenDay := func(utilization float64) QuotaWindow {
		return QuotaWindow{Key: "seven_day", Label: "7-day", Utilization: utilization, ResetsAt: thursday, Weekly: true, Routing: true}
	}
	fable := func(utilization float64, resetsAt time.Time) QuotaWindow {
		return QuotaWindow{Key: "iguana_necktie", Label: "7-day Fable", Utilization: utilization, ResetsAt: resetsAt, Weekly: true, ModelScope: "fable", Routing: true}
	}
	display := QuotaWindow{Key: "seven_day_cowork", Label: "7-day Cowork", Utilization: 100, ResetsAt: thursday, Weekly: true, Routing: false}

	tests := []struct {
		name  string
		snap  *QuotaSnapshot
		model string
		want  bool
	}{
		{
			name:  "fable exhausted while all-models window has headroom",
			snap:  &QuotaSnapshot{AuthID: "a", Provider: "claude", FetchedAt: fresh, Windows: []QuotaWindow{sevenDay(68), fable(100, thursday), display}},
			model: "claude-fable-5-1",
			want:  true,
		},
		{
			name:  "same snapshot but the request is for opus",
			snap:  &QuotaSnapshot{AuthID: "a", Provider: "claude", FetchedAt: fresh, Windows: []QuotaWindow{sevenDay(68), fable(100, thursday)}},
			model: "claude-opus-5-5",
			want:  false,
		},
		{
			name:  "all-models window is exhausted too",
			snap:  &QuotaSnapshot{AuthID: "a", Provider: "claude", FetchedAt: fresh, Windows: []QuotaWindow{sevenDay(100), fable(100, thursday)}},
			model: "claude-fable-5-1",
			want:  false,
		},
		{
			name:  "fable window below the limit",
			snap:  &QuotaSnapshot{AuthID: "a", Provider: "claude", FetchedAt: fresh, Windows: []QuotaWindow{sevenDay(68), fable(97, thursday)}},
			model: "claude-fable-5-1",
			want:  false,
		},
		{
			name:  "fable window already reset",
			snap:  &QuotaSnapshot{AuthID: "a", Provider: "claude", FetchedAt: fresh, Windows: []QuotaWindow{sevenDay(68), fable(100, now.Add(-time.Minute))}},
			model: "claude-fable-5-1",
			want:  false,
		},
		{
			name:  "stale snapshot is not trusted",
			snap:  &QuotaSnapshot{AuthID: "a", Provider: "claude", FetchedAt: now.Add(-20 * time.Minute), Windows: []QuotaWindow{sevenDay(68), fable(100, thursday)}},
			model: "claude-fable-5-1",
			want:  false,
		},
		{
			name:  "no snapshot",
			snap:  nil,
			model: "claude-fable-5-1",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewQuotaSnapshotStore()
			if tt.snap != nil {
				store.Put(*tt.snap)
			}
			if got := store.ModelOnlyLimit("a", tt.model, now); got != tt.want {
				t.Fatalf("ModelOnlyLimit(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}
