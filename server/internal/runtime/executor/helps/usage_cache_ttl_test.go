package helps

import "testing"

func TestParseClaudeUsageKeepsOneHourCacheWritesAndSpeed(t *testing.T) {
	detail := ParseClaudeUsage([]byte(`{"usage":{"input_tokens":2,"output_tokens":413,
		"cache_read_input_tokens":37023,"cache_creation_input_tokens":40849,
		"cache_creation":{"ephemeral_5m_input_tokens":849,"ephemeral_1h_input_tokens":40000},
		"speed":"fast"}}`))
	if detail.CacheCreationTokens != 40849 || detail.CacheCreation1hTokens != 40000 {
		t.Fatalf("cache writes = %d (1h %d), want 40849 (1h 40000)", detail.CacheCreationTokens, detail.CacheCreation1hTokens)
	}
	if detail.Speed != "fast" {
		t.Fatalf("speed = %q, want fast", detail.Speed)
	}
}

func TestClaudeStreamUsageMergeCarriesCacheTTLAndSpeed(t *testing.T) {
	buffer := &StreamUsageBuffer{}
	buffer.ObserveClaudeStream([]byte(`data: {"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":3,` +
		`"cache_read_input_tokens":100,"cache_creation_input_tokens":500,"cache_creation":{"ephemeral_1h_input_tokens":500},"output_tokens":1,"speed":"standard"}}}`))
	buffer.ObserveClaudeStream([]byte(`data: {"type":"message_delta","usage":{"output_tokens":250}}`))
	detail, ok := buffer.Detail()
	if !ok {
		t.Fatal("no usage observed")
	}
	if detail.OutputTokens != 250 || detail.CacheCreationTokens != 500 || detail.CacheCreation1hTokens != 500 || detail.Speed != "standard" {
		t.Fatalf("merged detail = %+v", detail)
	}
}
