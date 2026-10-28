package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewEndpointModelList(t *testing.T) {
	handler := newServerHandler("../../prototype")
	body := `{"base_url":"api.example.com","api_key":"sk-test","kind":"curl"}`
	req := httptest.NewRequest(http.MethodPost, "/api/preview", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(payload.Preview, "https://api.example.com/v1/models") {
		t.Fatalf("preview missing model list URL:\n%s", payload.Preview)
	}
}

func TestPreviewEndpointConversationTask(t *testing.T) {
	handler := newServerHandler("../../prototype")
	body := `{"base_url":"https://api.example.com/v1","api_key":"","kind":"post","stream":true,"tasks":[{"root":"https://api.example.com/v1","protocol":"openai-chat","model":"gpt-4o-mini"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/preview", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(payload.Preview, "POST https://api.example.com/v1/chat/completions") {
		t.Fatalf("preview missing endpoint:\n%s", payload.Preview)
	}
	if !strings.Contains(payload.Preview, "Bearer <apiKey>") {
		t.Fatalf("preview missing <apiKey> placeholder:\n%s", payload.Preview)
	}
}

func TestAgentIdentityEndpoint(t *testing.T) {
	handler := newServerHandler("../../prototype")
	req := httptest.NewRequest(http.MethodPost, "/api/agent-identity", strings.NewReader(`{"system_prompt_preset":"codex"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Identity struct {
			OpenAI []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"openai"`
		} `json:"identity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var ua, originator string
	for _, header := range payload.Identity.OpenAI {
		switch header.Name {
		case "User-Agent":
			ua = header.Value
		case "originator":
			originator = header.Value
		}
	}
	if !strings.HasPrefix(ua, "codex_cli_rs/") || originator != "codex_cli_rs" {
		t.Fatalf("codex identity = %q / %q", ua, originator)
	}

	// 未知预设应 404
	req = httptest.NewRequest(http.MethodPost, "/api/agent-identity", strings.NewReader(`{"system_prompt_preset":"nope"}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown preset status = %d", rec.Code)
	}
}
