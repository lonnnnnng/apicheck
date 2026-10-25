package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"apicheck/internal/apireq"
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

func TestClientIdentityEndpoint(t *testing.T) {
	handler := newServerHandler("../../prototype")
	body := `{"openai_ua_surface":"cli","anthropic_ua_surface":"desktop"}`
	req := httptest.NewRequest(http.MethodPost, "/api/client-identity", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var identities map[string]struct {
		Protocol string `json:"protocol"`
		Headers  []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &identities); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(identities) != 2 {
		t.Fatalf("want 2 identities, got %d", len(identities))
	}
	found := false
	for _, header := range identities["anthropic"].Headers {
		if header.Name == "anthropic-client-platform" && header.Value == "claude_code_remote" {
			found = true
		}
	}
	if !found {
		t.Fatal("desktop anthropic identity missing claude_code_remote")
	}
	_ = apireq.ParsePreviewKind("post")
}
