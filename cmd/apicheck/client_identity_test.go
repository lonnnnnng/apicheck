package main

import "testing"

func TestConfigFromRequestMapsCustomClientIdentity(t *testing.T) {
	cfg := configFromRequest(checkRequest{
		OpenAIUserAgent:         "web-openai/1.0",
		OpenAIOriginator:        "web_origin",
		AnthropicUserAgent:      "web-anthropic/1.0",
		AnthropicClientPlatform: "web_remote",
	})
	if cfg.OpenAIUserAgent != "web-openai/1.0" || cfg.OpenAIOriginator != "web_origin" {
		t.Fatalf("OpenAI identity = %q / %q", cfg.OpenAIUserAgent, cfg.OpenAIOriginator)
	}
	if cfg.AnthropicUserAgent != "web-anthropic/1.0" || cfg.AnthropicClientPlatform != "web_remote" {
		t.Fatalf("Anthropic identity = %q / %q", cfg.AnthropicUserAgent, cfg.AnthropicClientPlatform)
	}
}
