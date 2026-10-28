package checker

import (
	"strings"
	"testing"
)

func TestPreviewCurlMatchesConversationRequest(t *testing.T) {
	cfg := Config{
		BaseURL: "https://api.example.com",
		APIKey:  "sk-test",
		Prompt:  "Reply with exactly: pong",
	}
	task := TaskSpec{Root: "https://api.example.com/v1", Protocol: ProtocolOpenAIChat, Model: "gpt-4o-mini"}
	text, err := Preview(cfg, task, false, PreviewCurl)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	for _, want := range []string{
		"curl -sS -X POST 'https://api.example.com/v1/chat/completions'",
		"-H 'Authorization: Bearer sk-test'",
		"\"model\":\"gpt-4o-mini\",\"stream\":false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("curl preview missing %q:\n%s", want, text)
		}
	}
}

func TestPreviewRedactsEmptyAPIKey(t *testing.T) {
	cfg := Config{BaseURL: "https://api.example.com", APIKey: "  "}
	task := TaskSpec{Root: "https://api.example.com/v1", Protocol: ProtocolAnthropicMessages, Model: "claude-3-5-haiku"}
	text, err := Preview(cfg, task, true, PreviewHTTP)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !strings.Contains(text, "X-Api-Key: <apiKey>") {
		t.Fatalf("anthropic preview missing <apiKey> placeholder:\n%s", text)
	}
	if !strings.Contains(text, "POST https://api.example.com/v1/messages") {
		t.Fatalf("anthropic preview missing endpoint:\n%s", text)
	}
	if !strings.Contains(text, "\"max_tokens\":") {
		t.Fatalf("anthropic preview missing max_tokens:\n%s", text)
	}
}

func TestPreviewShellQuote(t *testing.T) {
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Fatalf("shellQuote = %q", got)
	}
}

func TestModelListPreviewCoversBothAuthStyles(t *testing.T) {
	cfg := Config{BaseURL: "api.example.com", APIKey: "sk-test"}
	text, err := ModelListPreview(cfg, PreviewCurl)
	if err != nil {
		t.Fatalf("ModelListPreview: %v", err)
	}
	if !strings.Contains(text, "# OpenAI 风格（Authorization: Bearer）") {
		t.Fatal("missing openai section")
	}
	if !strings.Contains(text, "# Anthropic 风格（x-api-key）") {
		t.Fatal("missing anthropic section")
	}
	if !strings.Contains(text, "https://api.example.com/v1/models") {
		t.Fatalf("missing normalized root URL:\n%s", text)
	}
}

func TestAgentIdentityForPreset(t *testing.T) {
	preset, identity, ok := AgentIdentityForPreset("codex")
	if !ok || preset.ID != "codex" {
		t.Fatal("codex preset not found")
	}
	var ua, originator string
	for _, header := range identity.OpenAI {
		switch header.Name {
		case "User-Agent":
			ua = header.Value
		case "originator":
			originator = header.Value
		}
	}
	if originator != "codex_cli_rs" || !strings.Contains(ua, "codex_cli_rs/") {
		t.Fatalf("codex identity ua=%q originator=%q", ua, originator)
	}
	if _, identity, ok := AgentIdentityForPreset("deepseek"); ok && len(identity.OpenAI)+len(identity.Anthropic) > 0 {
		t.Fatal("deepseek should have no agent headers")
	}
}
