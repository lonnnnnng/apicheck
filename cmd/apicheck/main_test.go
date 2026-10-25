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
	"time"

	"apicheck/internal/apireq"
	"apicheck/internal/checker"
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

func TestConfigFromRequestHonorsStreamTimeouts(t *testing.T) {
	cfg := apireq.ConfigFrom(apireq.Request{
		Timeout:            "9s",
		ConnectTimeout:     "30s",
		StreamFirstTimeout: "11s",
		StreamIdleTimeout:  "12s",
		StreamMaxDuration:  "0",
	}, checker.ClientSurfaceCLI)
	if cfg.Timeout != 9*time.Second {
		t.Fatalf("timeout = %s, want 9s", cfg.Timeout)
	}
	if cfg.ConnectTimeout != 30*time.Second {
		t.Fatalf("connect timeout = %s, want 30s", cfg.ConnectTimeout)
	}
	if cfg.StreamFirstTimeout != 11*time.Second {
		t.Fatalf("stream first timeout = %s, want 11s", cfg.StreamFirstTimeout)
	}
	if cfg.StreamIdleTimeout != 12*time.Second {
		t.Fatalf("stream idle timeout = %s, want 12s", cfg.StreamIdleTimeout)
	}
	if cfg.StreamMaxDuration != 0 || !cfg.DisableStreamMax {
		t.Fatalf("stream max duration = %s disable=%v, want disabled", cfg.StreamMaxDuration, cfg.DisableStreamMax)
	}
}

func TestPresentationEndpoints(t *testing.T) {
	server := httptest.NewServer(newServerHandler("../../prototype"))
	defer server.Close()

	markdownBody, err := json.Marshal(textRequest{Text: "# 标题\n\n**内容**"})
	if err != nil {
		t.Fatal(err)
	}
	markdownResp, err := http.Post(server.URL+"/api/markdown", "application/json", bytes.NewReader(markdownBody))
	if err != nil {
		t.Fatal(err)
	}
	defer markdownResp.Body.Close()
	var markdownPayload struct {
		HTML string `json:"html"`
	}
	if err := json.NewDecoder(markdownResp.Body).Decode(&markdownPayload); err != nil {
		t.Fatal(err)
	}
	if markdownResp.StatusCode != http.StatusOK || !strings.Contains(markdownPayload.HTML, "<h1>标题</h1>") {
		t.Fatalf("markdown response status=%d payload=%+v", markdownResp.StatusCode, markdownPayload)
	}

	qrBody, err := json.Marshal(textRequest{Text: "https://example.com/v1,sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	qrResp, err := http.Post(server.URL+"/api/qrcode", "application/json", bytes.NewReader(qrBody))
	if err != nil {
		t.Fatal(err)
	}
	defer qrResp.Body.Close()
	var qrPayload struct {
		DataURL string `json:"data_url"`
	}
	if err := json.NewDecoder(qrResp.Body).Decode(&qrPayload); err != nil {
		t.Fatal(err)
	}
	if qrResp.StatusCode != http.StatusOK || !strings.HasPrefix(qrPayload.DataURL, "data:image/png;base64,") {
		t.Fatalf("QR response status=%d payload prefix=%q", qrResp.StatusCode, qrPayload.DataURL[:min(len(qrPayload.DataURL), 32)])
	}
}
