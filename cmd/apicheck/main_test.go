package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPICheckEndpoint(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer test-key" {
				http.Error(w, `{"error":{"message":"bad key","type":"auth_failed"}}`, http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"gpt-test"}]}`)
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"model":"gpt-test"`) {
				http.Error(w, `{"error":{"message":"bad model","type":"invalid_request"}}`, http.StatusBadRequest)
				return
			}
			if strings.Contains(string(body), `"stream":true`) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"pong"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	server := httptest.NewServer(newServerHandler("../../prototype"))
	defer server.Close()

	requestBody := []byte(fmt.Sprintf(`{
		"base_url": %q,
		"api_key": "test-key",
		"protocol": "openai-chat",
		"mode": "both",
		"concurrency": 1,
		"timeout": "5s"
	}`, provider.URL+"/v1"))
	resp, err := http.Post(server.URL+"/api/check", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var payload struct {
		Results []struct {
			Model     string `json:"model"`
			Protocol  string `json:"protocol"`
			NonStream struct {
				OK bool `json:"ok"`
			} `json:"nonstream"`
			Stream struct {
				OK          bool  `json:"ok"`
				FirstTextMS int64 `json:"first_text_ms"`
			} `json:"stream"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(payload.Results))
	}
	result := payload.Results[0]
	if result.Model != "gpt-test" || result.Protocol != "openai-chat" {
		t.Fatalf("unexpected result metadata: %+v", result)
	}
	if !result.NonStream.OK || !result.Stream.OK || result.Stream.FirstTextMS <= 0 {
		t.Fatalf("unexpected probe result: %+v", result)
	}
}

func TestAPIModelsThenCheckTasksEndpoint(t *testing.T) {
	modelCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			modelCalls++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"gpt-split"}]}`)
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "custom probe prompt") {
				http.Error(w, `{"error":{"message":"missing prompt","type":"invalid_request"}}`, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"pong"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	server := httptest.NewServer(newServerHandler("../../prototype"))
	defer server.Close()

	modelReq := []byte(fmt.Sprintf(`{
		"base_url": %q,
		"api_key": "test-key",
		"protocol": "openai-chat",
		"mode": "nonstream",
		"timeout": "5s"
	}`, provider.URL+"/v1"))
	modelResp, err := http.Post(server.URL+"/api/models", "application/json", bytes.NewReader(modelReq))
	if err != nil {
		t.Fatal(err)
	}
	defer modelResp.Body.Close()
	if modelResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(modelResp.Body)
		t.Fatalf("models status = %d, body = %s", modelResp.StatusCode, body)
	}
	var modelPayload struct {
		Tasks []struct {
			Root     string `json:"root"`
			Protocol string `json:"protocol"`
			Model    string `json:"model"`
		} `json:"tasks"`
		Results []struct{} `json:"results"`
	}
	if err := json.NewDecoder(modelResp.Body).Decode(&modelPayload); err != nil {
		t.Fatal(err)
	}
	if len(modelPayload.Tasks) != 1 || len(modelPayload.Results) != 0 {
		t.Fatalf("unexpected models payload: %+v", modelPayload)
	}

	checkReq := []byte(fmt.Sprintf(`{
		"base_url": %q,
		"api_key": "test-key",
		"prompt": "custom probe prompt",
		"protocol": "openai-chat",
		"mode": "nonstream",
		"timeout": "5s",
		"tasks": [{"root": %q, "protocol": "openai-chat", "model": "gpt-split"}]
	}`, provider.URL+"/v1", provider.URL+"/v1"))
	checkResp, err := http.Post(server.URL+"/api/check", "application/json", bytes.NewReader(checkReq))
	if err != nil {
		t.Fatal(err)
	}
	defer checkResp.Body.Close()
	if checkResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(checkResp.Body)
		t.Fatalf("check status = %d, body = %s", checkResp.StatusCode, body)
	}
	var checkPayload struct {
		Results []struct {
			Model     string `json:"model"`
			NonStream struct {
				OK bool `json:"ok"`
			} `json:"nonstream"`
		} `json:"results"`
	}
	if err := json.NewDecoder(checkResp.Body).Decode(&checkPayload); err != nil {
		t.Fatal(err)
	}
	if modelCalls != 1 {
		t.Fatalf("model list was called %d times, want 1", modelCalls)
	}
	if len(checkPayload.Results) != 1 || checkPayload.Results[0].Model != "gpt-split" || !checkPayload.Results[0].NonStream.OK {
		t.Fatalf("unexpected check payload: %+v", checkPayload)
	}
}
