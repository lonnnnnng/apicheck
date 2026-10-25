package main

import (
	"testing"

	"apicheck/internal/apireq"
	"apicheck/internal/checker"
)

func TestConfigFromRequestMapsCustomClientIdentity(t *testing.T) {
	cfg := apireq.ConfigFrom(apireq.Request{
		OpenAIUserAgent:         "web-openai/1.0",
		OpenAIOriginator:        "web_origin",
		AnthropicUserAgent:      "web-anthropic/1.0",
		AnthropicClientPlatform: "web_remote",
	}, checker.ClientSurfaceCLI)
	if cfg.OpenAIUserAgent != "web-openai/1.0" || cfg.OpenAIOriginator != "web_origin" {
		t.Fatalf("OpenAI identity = %q / %q", cfg.OpenAIUserAgent, cfg.OpenAIOriginator)
	}
	if cfg.AnthropicUserAgent != "web-anthropic/1.0" || cfg.AnthropicClientPlatform != "web_remote" {
		t.Fatalf("Anthropic identity = %q / %q", cfg.AnthropicUserAgent, cfg.AnthropicClientPlatform)
	}
}

func TestReportHasFailures(t *testing.T) {
	if reportHasFailures(&checker.Report{}) {
		t.Fatal("empty report should have no failures")
	}
	okReport := &checker.Report{Results: []checker.ModelResult{{Model: "m", NonStream: checker.ProbeResult{Tested: true, OK: true}}}}
	if reportHasFailures(okReport) {
		t.Fatal("all-ok report should have no failures")
	}
	failReport := &checker.Report{Results: []checker.ModelResult{
		{Model: "ok-model", NonStream: checker.ProbeResult{Tested: true, OK: true}},
		{Model: "bad-model", NonStream: checker.ProbeResult{Tested: true, OK: false, ErrorClass: "auth_failed"}},
	}}
	if !reportHasFailures(failReport) {
		t.Fatal("report with failed probe should be flagged")
	}
	skippedReport := &checker.Report{Results: []checker.ModelResult{{Model: "m", NonStream: checker.ProbeResult{Tested: false}}}}
	if reportHasFailures(skippedReport) {
		t.Fatal("untested probes should not count as failures")
	}
}
