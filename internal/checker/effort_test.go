package checker

import (
	"encoding/json"
	"strings"
	"testing"
)

// 模型等级（reasoning effort）在三个协议里字段名不同，这里逐个断言。
func TestConversationBodyCarriesReasoningEffort(t *testing.T) {
	cases := []struct {
		protocol Protocol
		effort   string
		want     func(t *testing.T, body map[string]any)
	}{
		{
			protocol: ProtocolOpenAIChat,
			effort:   ReasoningEffortHigh,
			want: func(t *testing.T, body map[string]any) {
				if body["reasoning_effort"] != "high" {
					t.Fatalf("openai-chat reasoning_effort 期望 high, 实际 %v", body["reasoning_effort"])
				}
			},
		},
		{
			protocol: ProtocolOpenAIResponses,
			effort:   ReasoningEffortLow,
			want: func(t *testing.T, body map[string]any) {
				reasoning, ok := body["reasoning"].(map[string]any)
				if !ok {
					t.Fatalf("openai-responses 缺少 reasoning 对象: %v", body["reasoning"])
				}
				if reasoning["effort"] != "low" {
					t.Fatalf("reasoning.effort 期望 low, 实际 %v", reasoning["effort"])
				}
			},
		},
		{
			protocol: ProtocolAnthropicMessages,
			effort:   ReasoningEffortMedium,
			want: func(t *testing.T, body map[string]any) {
				config, ok := body["output_config"].(map[string]any)
				if !ok {
					t.Fatalf("anthropic 缺少 output_config 对象: %v", body["output_config"])
				}
				if config["effort"] != "medium" {
					t.Fatalf("output_config.effort 期望 medium, 实际 %v", config["effort"])
				}
			},
		},
		{
			// anthropic 不认 minimal，降级成 low。
			protocol: ProtocolAnthropicMessages,
			effort:   ReasoningEffortMinimal,
			want: func(t *testing.T, body map[string]any) {
				config, _ := body["output_config"].(map[string]any)
				if config["effort"] != ReasoningEffortLow {
					t.Fatalf("anthropic minimal 期望降级为 low, 实际 %v", config["effort"])
				}
			},
		},
	}
	for _, tc := range cases {
		raw, err := conversationBodyWithEffort(tc.protocol, "m", false, "ping", "", 0, tc.effort)
		if err != nil {
			t.Fatalf("%s: %v", tc.protocol, err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("%s: %v", tc.protocol, err)
		}
		tc.want(t, body)
	}
}

// 不设置等级时不能凭空多出字段，否则会破坏对参数校验严格的网关。
func TestConversationBodyOmitsReasoningEffortWhenUnset(t *testing.T) {
	for _, protocol := range []Protocol{ProtocolOpenAIChat, ProtocolOpenAIResponses, ProtocolAnthropicMessages} {
		for _, effort := range []string{"", "none", "off", "不存在的等级"} {
			raw, err := conversationBodyWithEffort(protocol, "m", false, "ping", "", 0, effort)
			if err != nil {
				t.Fatalf("%s: %v", protocol, err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("%s: %v", protocol, err)
			}
			for _, key := range []string{"reasoning_effort", "reasoning", "output_config"} {
				if _, exists := body[key]; exists {
					t.Fatalf("%s effort=%q: 不应出现 %s 字段", protocol, effort, key)
				}
			}
		}
	}
}

// 预览与真实请求共用构造器，等级必须同时出现在预览里。
func TestPreviewCarriesReasoningEffort(t *testing.T) {
	cfg := Config{
		BaseURL:         "https://api.example.com/v1",
		APIKey:          "sk-test",
		Prompt:          "ping",
		ReasoningEffort: ReasoningEffortHigh,
		MaxTokens:       256,
	}
	text, err := Preview(cfg, TaskSpec{Root: "https://api.example.com/v1", Model: "gpt-5", Protocol: ProtocolOpenAIResponses}, false, PreviewCurl)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	for _, want := range []string{`"reasoning"`, `"effort"`, `"high"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("预览缺少 %s:\n%s", want, text)
		}
	}
}
