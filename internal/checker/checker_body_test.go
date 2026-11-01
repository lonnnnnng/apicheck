package checker

import (
	"encoding/json"
	"strings"
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
	// new-api 系网关只认数组形态的 input，传字符串会回 400 "input is required"。
	input, ok := body["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input 期望数组形态, 实际 %#v", body["input"])
	}
	item, _ := input[0].(map[string]any)
	if item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("input[0] 期望 user message, 实际 %#v", item)
	}
	content, _ := item["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("input[0].content 期望 1 段, 实际 %#v", item["content"])
	}
	part, _ := content[0].(map[string]any)
	if part["type"] != "input_text" || part["text"] != "ping" {
		t.Fatalf("content[0] 期望 input_text/ping, 实际 %#v", part)
	}
	if body["store"] != false {
		t.Fatalf("store 期望 false, 实际 %#v", body["store"])
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

func TestConversationBodyHonoursMaxTokens(t *testing.T) {
	cases := []Protocol{ProtocolOpenAIResponses, ProtocolAnthropicMessages}
	for _, protocol := range cases {
		raw, err := conversationBodyWithOptions(protocol, "m", false, "ping", "", 777)
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		got := body["max_output_tokens"]
		if protocol == ProtocolAnthropicMessages {
			got = body["max_tokens"]
		}
		if got != float64(777) {
			t.Fatalf("%s: 期望 777, 实际 %v", protocol, got)
		}
		// 0 表示回退默认值
		raw, err = conversationBodyWithOptions(protocol, "m", false, "ping", "", 0)
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		body = map[string]any{}
		_ = json.Unmarshal(raw, &body)
		got = body["max_output_tokens"]
		if protocol == ProtocolAnthropicMessages {
			got = body["max_tokens"]
		}
		if got != float64(defaultProbeMaxTokens) {
			t.Fatalf("%s: 期望默认 %d, 实际 %v", protocol, defaultProbeMaxTokens, got)
		}
	}
}

// 预览与真实请求必须用同一个输出上限，否则用户看到的 curl 与实际发送不一致。
func TestPreviewCarriesConfiguredMaxTokens(t *testing.T) {
	cfg := Config{
		BaseURL:   "https://example.com/v1",
		APIKey:    "k",
		Protocol:  ProtocolOpenAIResponses,
		Mode:      ModeStream,
		Prompt:    "ping",
		MaxTokens: 1234,
	}
	text, err := Preview(cfg, TaskSpec{Root: "https://example.com/v1", Protocol: ProtocolOpenAIResponses, Model: "m"}, true, PreviewCurl)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !strings.Contains(text, `"max_output_tokens":1234`) {
		t.Fatalf("预览未带上 max_tokens:\n%s", text)
	}
}
