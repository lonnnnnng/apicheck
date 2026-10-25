package checker

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemPromptPresets(t *testing.T) {
	presets := SystemPromptPresets()
	if len(presets) != len(presetMeta) {
		t.Fatalf("presets = %d, want %d", len(presets), len(presetMeta))
	}
	if presets[0].ID != "none" {
		t.Fatalf("first preset = %q, want none", presets[0].ID)
	}
	if presets[0].SystemPrompt != "" {
		t.Fatalf("none preset should carry an empty system prompt, got %q", presets[0].SystemPrompt)
	}
	for _, preset := range presets[1:] {
		if strings.TrimSpace(preset.SystemPrompt) == "" {
			t.Errorf("preset %s has empty system prompt", preset.ID)
		}
		if preset.Name == "" || preset.Description == "" {
			t.Errorf("preset %s missing name or description", preset.ID)
		}
	}
}

func TestSystemPromptByID(t *testing.T) {
	if _, ok := SystemPromptByID("no-such-preset"); ok {
		t.Fatal("unknown preset should not resolve")
	}
	none, ok := SystemPromptByID("")
	if !ok || none.ID != "none" {
		t.Fatalf("empty id = %+v, %v; want none preset", none, ok)
	}
	pi, ok := SystemPromptByID("pi")
	if !ok || !strings.Contains(pi.SystemPrompt, "pi") {
		t.Fatalf("pi preset missing or body unexpected: %+v", pi)
	}
}

func TestConversationBodyInjectsSystemPrompt(t *testing.T) {
	const system = "You are a coding agent."

	chat, err := conversationBodyWithSystem(ProtocolOpenAIChat, "m", false, "ping", system)
	if err != nil {
		t.Fatal(err)
	}
	var chatBody struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(chat, &chatBody); err != nil {
		t.Fatal(err)
	}
	if len(chatBody.Messages) != 2 || chatBody.Messages[0].Role != "system" || chatBody.Messages[0].Content != system {
		t.Fatalf("chat messages = %+v", chatBody.Messages)
	}

	responses, err := conversationBodyWithSystem(ProtocolOpenAIResponses, "m", false, "ping", system)
	if err != nil {
		t.Fatal(err)
	}
	var responsesBody map[string]any
	if err := json.Unmarshal(responses, &responsesBody); err != nil {
		t.Fatal(err)
	}
	if responsesBody["instructions"] != system {
		t.Fatalf("responses instructions = %v", responsesBody["instructions"])
	}

	anthropic, err := conversationBodyWithSystem(ProtocolAnthropicMessages, "m", false, "ping", system)
	if err != nil {
		t.Fatal(err)
	}
	var anthropicBody map[string]any
	if err := json.Unmarshal(anthropic, &anthropicBody); err != nil {
		t.Fatal(err)
	}
	if anthropicBody["system"] != system {
		t.Fatalf("anthropic system = %v", anthropicBody["system"])
	}

	// 无 system prompt 时保持旧行为：chat messages 只有一条 user。
	plain, err := conversationBodyWithSystem(ProtocolOpenAIChat, "m", false, "ping", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(plain, &chatBody); err != nil {
		t.Fatal(err)
	}
	if len(chatBody.Messages) != 1 || chatBody.Messages[0].Role != "user" {
		t.Fatalf("plain chat messages = %+v", chatBody.Messages)
	}
}

func TestPreviewCarriesSystemPrompt(t *testing.T) {
	cfg := Config{
		SystemPromptPresetID: "pi",
		Prompt:               "ping",
	}
	task := TaskSpec{Root: "https://api.example.com/v1", Protocol: ProtocolOpenAIChat, Model: "m"}
	text, err := Preview(cfg, task, false, PreviewCurl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "You are an expert coding assistant") {
		t.Fatalf("preview missing pi system prompt:\n%s", text)
	}

	// none / 未知预设都不应注入 system 内容。
	cfg.SystemPromptPresetID = "none"
	text, err = Preview(cfg, task, false, PreviewCurl)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "You are an expert coding assistant") {
		t.Fatal("none preset should not inject a system prompt")
	}
}
