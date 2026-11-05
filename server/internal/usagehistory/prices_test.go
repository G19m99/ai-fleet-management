package usagehistory

import (
	"math"
	"testing"
)

func approx(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %.10f, want %.10f", got, want)
	}
}

func TestCostOpus55AllBuckets(t *testing.T) {
	r := Record{Model: "claude-opus-5-5", In: 1_000_000, Out: 1_000_000, CacheRead: 1_000_000, CacheWrite5m: 1_000_000, CacheWrite1h: 1_000_000}
	got, ok := Cost(r)
	if !ok {
		t.Fatal("opus 5.5 should be priced")
	}
	approx(t, got, 4+20+0.20+5+8)
}

func TestCostHaikuTierByPromptSize(t *testing.T) {
	small := Record{Model: "claude-haiku-5-5", In: 50_000, CacheRead: 50_000, Out: 1_000}
	got, _ := Cost(small)
	approx(t, got, (50_000*0.10+50_000*0.01+1_000*0.50)/1e6)

	big := Record{Model: "claude-haiku-5-5", In: 50_000, CacheRead: 50_001, Out: 1_000}
	got, _ = Cost(big)
	approx(t, got, (50_000*0.50+50_001*0.05+1_000*2.50)/1e6)
}

func TestCostFastModeOnlyOnFastModels(t *testing.T) {
	fast := Record{Model: "claude-opus-5-5", Out: 1_000_000, Speed: "fast"}
	got, _ := Cost(fast)
	approx(t, got, 40)

	sonnet := Record{Model: "claude-sonnet-5-5", Out: 1_000_000, Speed: "fast"}
	got, _ = Cost(sonnet)
	approx(t, got, 10)
}

func TestCostNormalizesModelAndRejectsUnknown(t *testing.T) {
	got, ok := Cost(Record{Model: "claude-haiku-4-5-20251001", Out: 1_000_000})
	if !ok {
		t.Fatal("dated haiku 4.5 should be priced")
	}
	approx(t, got, 5)
	if _, ok = Cost(Record{Model: "claude-opus-5-5[1m]", In: 1}); !ok {
		t.Fatal("[1m] suffix should be stripped")
	}
	if _, ok = Cost(Record{Model: "gpt-5.5-codex", In: 1000}); ok {
		t.Fatal("codex models have no price")
	}
}
