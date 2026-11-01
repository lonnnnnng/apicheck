package checker

import (
	"encoding/json"
	"testing"
)

// 作者 long: anyrouter 这类 new-api 网关在 /responses 上缺 prompt_cache_key 会直接 400，
// 这里锁住三种协议各自的请求体形状，避免以后改 body 时又把这层兼容性丢掉。
func TestResponsesBodyCarriesPromptCacheKey(t *testing.T) {
	raw, err := conversationBody(ProtocolOpenAIResponses, "gpt-6.1-sol", true, "ping")
	if err != nil {
		t.Fatalf("build body: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	key, _ := body["prompt_cache_key"].(string)
	if key == "" {
		t.Fatalf("prompt_cache_key missing: %s", raw)
	}
	if len(key) != 36 || key[8] != '-' || key[13] != '-' || key[18] != '-' || key[23] != '-' {
		t.Fatalf("prompt_cache_key 不是 UUID 形状: %q", key)
	}
	if body["max_output_tokens"] != float64(defaultProbeMaxTokens) {
		t.Fatalf("max_output_tokens 期望 %d, 实际 %v", defaultProbeMaxTokens, body["max_output_tokens"])
	}
	if body["stream"] != true {
		t.Fatalf("stream 期望 true, 实际 %v", body["stream"])
	}
}

func TestPromptCacheKeyIsStablePerModel(t *testing.T) {
	if a, b := probePromptCacheKey("gpt-6.1-sol"), probePromptCacheKey("gpt-6.1-sol"); a != b {
		t.Fatalf("同一模型应得到同一个 key: %s vs %s", a, b)
	}
	if probePromptCacheKey("gpt-6.1-sol") == probePromptCacheKey("gpt-5-codex") {
		t.Fatal("不同模型应得到不同 key")
	}
}

// chat 与 anthropic 协议没有这个字段，别把 responses 的兼容补丁扩散过去。
func TestPromptCacheKeyOnlyOnResponses(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAIChat, ProtocolAnthropicMessages} {
		raw, err := conversationBody(protocol, "m", false, "ping")
		if err != nil {
			t.Fatalf("%s build body: %v", protocol, err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("%s decode body: %v", protocol, err)
		}
		if _, ok := body["prompt_cache_key"]; ok {
			t.Fatalf("%s 不应该带 prompt_cache_key: %s", protocol, raw)
		}
	}
}
