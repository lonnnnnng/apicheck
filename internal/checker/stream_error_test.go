package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExtractStreamEventError(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantOK  bool
		wantCod string
		wantMsg string
	}{
		{
			name:    "responses failed",
			body:    `{"type":"response.failed","sequence_number":9,"response":{"id":"resp_1","status":"failed","error":{"code":"429","message":"上游排队中"}}}`,
			wantOK:  true,
			wantCod: "429",
			wantMsg: "上游排队中",
		},
		{
			name:    "responses error event",
			body:    `{"type":"error","error":{"type":"server_error","message":"boom"}}`,
			wantOK:  true,
			wantCod: "server_error",
			wantMsg: "boom",
		},
		{
			name:    "anthropic error event",
			body:    `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			wantOK:  true,
			wantCod: "overloaded_error",
			wantMsg: "Overloaded",
		},
		{
			name:    "openai chat error chunk",
			body:    `{"error":{"message":"quota exceeded","code":"insufficient_quota"}}`,
			wantOK:  true,
			wantCod: "insufficient_quota",
			wantMsg: "quota exceeded",
		},
		{
			name:    "responses incomplete",
			body:    `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`,
			wantOK:  true,
			wantCod: "incomplete",
			wantMsg: "响应不完整：max_output_tokens",
		},
		{
			name:   "in progress is not an error",
			body:   `{"type":"response.in_progress","response":{"status":"in_progress"}}`,
			wantOK: false,
		},
		{
			name:   "normal delta is not an error",
			body:   `{"choices":[{"delta":{"content":"pong"}}]}`,
			wantOK: false,
		},
	}
	for _, testCase := range cases {
		code, message, ok := extractStreamEventError([]byte(testCase.body))
		if ok != testCase.wantOK {
			t.Fatalf("%s: ok = %v, want %v (%q/%q)", testCase.name, ok, testCase.wantOK, code, message)
		}
		if !ok {
			continue
		}
		if code != testCase.wantCod || message != testCase.wantMsg {
			t.Fatalf("%s: got (%q, %q), want (%q, %q)", testCase.name, code, message, testCase.wantCod, testCase.wantMsg)
		}
	}
}

func TestClassifyStreamEventError(t *testing.T) {
	cases := []struct{ code, message, want string }{
		{"429", "", "rate_limited"},
		{"403", "", "auth_failed"},
		{"401", "", "auth_failed"},
		{"404", "", "endpoint_not_found"},
		{"503", "", "server_error"},
		{"400", "", "invalid_request"},
		{"", "上游排队中（10605 / RESOURCE_LIMIT）", "rate_limited"},
		{"overloaded_error", "Overloaded", "rate_limited"},
		{"insufficient_quota", "quota exceeded", "rate_limited"},
		{"custom_gateway_code", "", "custom_gateway_code"},
		{"", "", "stream_error"},
	}
	for _, testCase := range cases {
		if got := classifyStreamEventError(testCase.code, testCase.message); got != testCase.want {
			t.Fatalf("classify(%q, %q) = %q, want %q", testCase.code, testCase.message, got, testCase.want)
		}
	}
}

func TestSanitizeErrorMessageFlattensNestedJSON(t *testing.T) {
	raw := `上游排队中（10605 / RESOURCE_LIMIT）：已按 retryAfterSeconds 等待并重试，仍拿不到可用资源。请稍后重试；若频繁出现，说明该模型当前排队严重。上游详情：{"code":"403","message":"{\"code\":\"10605\",\"message\":\"{\\\"isQueued\\\":true,\\\"modelKey\\\":\\\"dfmodel\\\",\\\"queueCount\\\":0,\\\"queueType\\\":\\\"p3\\\",\\\"retryAfterSeconds\\\":30}\"}"}`
	got := sanitizeErrorMessage(raw)
	if !strings.Contains(got, "上游排队中") {
		t.Fatalf("missing readable prefix: %q", got)
	}
	if !strings.Contains(got, "403 → 10605") {
		t.Fatalf("missing flattened codes: %q", got)
	}
	if !strings.Contains(got, `"isQueued":true`) {
		t.Fatalf("missing innermost detail: %q", got)
	}
	if strings.Contains(got, `\"`) {
		t.Fatalf("escaped quotes should be decoded: %q", got)
	}
	if strings.Count(got, "\n") > 3 {
		t.Fatalf("error message should stay compact: %q", got)
	}
}

func TestSanitizeErrorMessageKeepsPlainText(t *testing.T) {
	if got := sanitizeErrorMessage("  model cannot stream  "); got != "model cannot stream" {
		t.Fatalf("plain message = %q", got)
	}
	if got := sanitizeErrorMessage(""); got != "" {
		t.Fatalf("empty message = %q", got)
	}
	long := strings.Repeat("错", errorMessageLimitRunes+50)
	got := sanitizeErrorMessage(long)
	if !strings.HasSuffix(got, "…（错误信息已截断）") {
		t.Fatalf("long message should be truncated: %q", got[len(got)-30:])
	}
}

func TestLooksLikeEventStream(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"event: response.created\ndata: {\"type\":\"response.created\"}\n", true},
		{"data: {\"choices\":[]}\n\ndata: [DONE]\n", true},
		{"\n\nevent: message_start\n", true},
		{`{"error":{"message":"bad key"}}`, false},
		{"pong", false},
		{"", false},
	}
	for _, testCase := range cases {
		if got := looksLikeEventStream(testCase.raw); got != testCase.want {
			t.Fatalf("looksLikeEventStream(%q) = %v, want %v", testCase.raw, got, testCase.want)
		}
	}
}

// TestStreamFailedEventMarksProbeFailed 覆盖网关先回 200 握手、再用 response.failed
// 宣告失败的场景：必须判定为失败，且预览区不能是整段 SSE 原文。
func TestStreamFailedEventMarksProbeFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\",\"model\":\"DeepSeek-Flash\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":{\"code\":\"429\",\"message\":\"上游排队中（10605 / RESOURCE_LIMIT）\"}}}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	report, err := CheckModels(context.Background(), Config{
		BaseURL:  server.URL + "/v1",
		APIKey:   "test-key",
		Protocol: ProtocolOpenAIResponses,
		Mode:     ModeStream,
		Timeout:  5 * time.Second,
	}, []TaskSpec{{Root: server.URL + "/v1", Protocol: ProtocolOpenAIResponses, Model: "DeepSeek-Flash"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(report.Results))
	}
	stream := report.Results[0].Stream
	if stream.OK {
		t.Fatalf("stream with response.failed must not be ok: %+v", stream)
	}
	if stream.ErrorClass != "rate_limited" {
		t.Fatalf("error class = %q, want rate_limited", stream.ErrorClass)
	}
	if !strings.Contains(stream.ErrorMessage, "上游排队中") {
		t.Fatalf("error message = %q", stream.ErrorMessage)
	}
	if looksLikeEventStream(stream.Preview) {
		t.Fatalf("preview should not be a raw SSE dump: %q", stream.Preview)
	}
	if !strings.Contains(stream.RawResponse, "response.failed") {
		t.Fatalf("raw response should keep the SSE body: %q", stream.RawResponse)
	}
}

// TestStreamWithoutTextKeepsRawPreviewForJSONBody 确认非 SSE 的 200 响应仍然可以
// 作为预览展示（网关有时忽略 stream=true 直接回普通 JSON）。
func TestStreamWithoutTextKeepsRawPreviewForJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-1","choices":[{"message":{"content":""}}]}`)
	}))
	defer server.Close()

	report, err := CheckModels(context.Background(), Config{
		BaseURL:  server.URL + "/v1",
		APIKey:   "test-key",
		Protocol: ProtocolOpenAIChat,
		Mode:     ModeStream,
		Timeout:  5 * time.Second,
	}, []TaskSpec{{Root: server.URL + "/v1", Protocol: ProtocolOpenAIChat, Model: "gpt-test"}})
	if err != nil {
		t.Fatal(err)
	}
	stream := report.Results[0].Stream
	if !stream.OK {
		t.Fatalf("stream unexpectedly failed: %+v", stream)
	}
	if !strings.Contains(stream.Preview, "chatcmpl-1") {
		t.Fatalf("preview = %q, want raw JSON body", stream.Preview)
	}
}
