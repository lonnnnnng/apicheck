package main

import (
	"testing"

	"apicheck/internal/apireq"
	"apicheck/internal/checker"
)

func TestConfigFromRequestMapsCustomClientIdentity(t *testing.T) {
	cfg := apireq.ConfigFrom(apireq.Request{
		OpenAIUserAgent:         "desktop-openai/1.0",
		OpenAIOriginator:        "desktop_origin",
		AnthropicUserAgent:      "desktop-anthropic/1.0",
		AnthropicClientPlatform: "desktop_remote",
	}, checker.ClientSurfaceDesktop)
	if cfg.OpenAIUserAgent != "desktop-openai/1.0" || cfg.OpenAIOriginator != "desktop_origin" {
		t.Fatalf("OpenAI identity = %q / %q", cfg.OpenAIUserAgent, cfg.OpenAIOriginator)
	}
	if cfg.AnthropicUserAgent != "desktop-anthropic/1.0" || cfg.AnthropicClientPlatform != "desktop_remote" {
		t.Fatalf("Anthropic identity = %q / %q", cfg.AnthropicUserAgent, cfg.AnthropicClientPlatform)
	}
}
