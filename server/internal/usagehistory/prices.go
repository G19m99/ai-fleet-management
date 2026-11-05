package usagehistory

import (
	"regexp"
	"strings"
)

// Price is USD per million tokens for one model at standard speed.
type Price struct {
	Input, CacheWrite5m, CacheWrite1h, CacheRead, Output float64
}

// tieredPrice applies Above to requests whose prompt (all input tokens,
// including cache reads and writes) exceeds Threshold tokens.
type tieredPrice struct {
	Base      Price
	Threshold int64
	Above     Price
}

// fastMultiplier is the fast-mode premium on models that offer it; cache
// multipliers apply on top of the fast-mode base price.
const fastMultiplier = 2.0

// prices are Anthropic first-party API rates, checked against
// https://platform.claude.com/docs/en/about-claude/pricing on 2026-11-05.
// Cache writes are 1.25x (5m) and 2x (1h) of base input; cache reads are
// 0.1x, except 0.025x on Fable 5.1 / Mythos 5.1 and 0.05x on Opus 5.5 / Sonnet 5.5.
var prices = map[string]tieredPrice{
	"claude-fable-5-1":  {Base: Price{10, 12.5, 20, 0.25, 50}},
	"claude-mythos-5-1": {Base: Price{10, 12.5, 20, 0.25, 50}},
	"claude-fable-5":    {Base: Price{10, 12.5, 20, 1, 50}},
	"claude-opus-5-5":   {Base: Price{4, 5, 8, 0.20, 20}},
	"claude-opus-5":     {Base: Price{5, 6.25, 10, 0.50, 25}},
	"claude-opus-4-8":   {Base: Price{5, 6.25, 10, 0.50, 25}},
	"claude-opus-4-7":   {Base: Price{5, 6.25, 10, 0.50, 25}},
	"claude-opus-4-6":   {Base: Price{5, 6.25, 10, 0.50, 25}},
	"claude-sonnet-5-5": {Base: Price{2, 2.5, 4, 0.10, 10}},
	"claude-sonnet-5":   {Base: Price{2, 2.5, 4, 0.20, 10}},
	"claude-sonnet-4-6": {Base: Price{3, 3.75, 6, 0.30, 15}},
	"claude-haiku-5-5": {
		Base:      Price{0.10, 0.125, 0.20, 0.01, 0.50},
		Threshold: 100_000,
		Above:     Price{0.50, 0.625, 1, 0.05, 2.50},
	},
	"claude-haiku-4-5": {Base: Price{1, 1.25, 2, 0.10, 5}},
}

// fastModels offer fast mode at fastMultiplier times the standard price.
var fastModels = map[string]bool{
	"claude-opus-5-5": true,
	"claude-opus-5":   true,
	"claude-opus-4-8": true,
}

var dateSuffixRE = regexp.MustCompile(`-\d{8}$`)

// NormalizeModel maps a reported model id onto a price-table key:
// lowercase, no "[1m]"-style suffix, no "-YYYYMMDD" snapshot date.
func NormalizeModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, '['); i > 0 {
		m = m[:i]
	}
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	m = strings.TrimPrefix(m, "anthropic.")
	return dateSuffixRE.ReplaceAllString(m, "")
}

// Cost returns the API-equivalent USD cost of one record. ok is false when
// the model has no price (e.g. Codex models), in which case cost is 0.
func Cost(r Record) (usd float64, ok bool) {
	model := NormalizeModel(r.Model)
	tp, found := prices[model]
	if !found {
		return 0, false
	}
	p := tp.Base
	if tp.Threshold > 0 && r.PromptTokens() > tp.Threshold {
		p = tp.Above
	}
	mult := 1.0
	if strings.EqualFold(strings.TrimSpace(r.Speed), "fast") && fastModels[model] {
		mult = fastMultiplier
	}
	usd = (float64(r.In)*p.Input +
		float64(r.CacheWrite5m)*p.CacheWrite5m +
		float64(r.CacheWrite1h)*p.CacheWrite1h +
		float64(r.CacheRead)*p.CacheRead +
		float64(r.Out)*p.Output) * mult / 1e6
	return usd, true
}
