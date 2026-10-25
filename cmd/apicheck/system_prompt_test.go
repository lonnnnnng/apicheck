package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"apicheck/internal/apireq"
	"apicheck/internal/checker"
)

func TestSystemPromptsEndpoint(t *testing.T) {
	handler := newServerHandler("../../prototype")
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := http.Get(server.URL + "/api/system-prompts")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var presets []map[string]any
	if err := json.NewDecoder(response.Body).Decode(&presets); err != nil {
		t.Fatal(err)
	}
	if len(presets) < 5 {
		t.Fatalf("presets = %d, want >= 5", len(presets))
	}
	if presets[0]["id"] != "none" {
		t.Fatalf("first preset = %v, want none", presets[0]["id"])
	}
	ids := map[string]bool{}
	for _, preset := range presets {
		ids[preset["id"].(string)] = true
	}
	for _, want := range []string{"pi", "codex", "codex-gpt-5", "opencode", "deepseek", "deepseek-web"} {
		if !ids[want] {
			t.Errorf("preset %s missing from endpoint response", want)
		}
	}
}

func TestPreviewEndpointCarriesSystemPrompt(t *testing.T) {
	handler := newServerHandler("../../prototype")
	server := httptest.NewServer(handler)
	defer server.Close()

	body := `{"base_url":"https://api.example.com/v1","api_key":"sk-t","kind":"curl","system_prompt_preset":"pi","tasks":[{"root":"https://api.example.com/v1","protocol":"openai-chat","model":"gpt-4o-mini"}]}`
	response, err := http.Post(server.URL+"/api/preview", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload struct {
		Preview string `json:"preview"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Preview, "You are an expert coding assistant") {
		t.Fatalf("preview missing pi system prompt:\n%s", payload.Preview)
	}
}

func TestConfigFromMapsSystemPromptPreset(t *testing.T) {
	cfg := apireq.ConfigFrom(apireq.Request{SystemPromptPresetID: " opencode "}, checker.ClientSurfaceCLI)
	if cfg.SystemPromptPresetID != "opencode" {
		t.Fatalf("SystemPromptPresetID = %q", cfg.SystemPromptPresetID)
	}
}
