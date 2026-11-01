package checker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCustomHeaders(t *testing.T) {
	text := "# 注释行\nx-gateway-token: abc123\n  x-title :  My App  \n\nbad-line-no-colon\nx-bad name: v\n: empty-name"
	headers := parseCustomHeaders(text)
	want := map[string]string{"x-gateway-token": "abc123", "x-title": "My App"}
	if len(headers) != len(want) {
		t.Fatalf("期望 %d 条, 实际 %d: %#v", len(want), len(headers), headers)
	}
	for _, header := range headers {
		if want[header.name] != header.value {
			t.Fatalf("头 %q 值不符: %q", header.name, header.value)
		}
	}
}

func TestParseCustomHeadersEmpty(t *testing.T) {
	if headers := parseCustomHeaders("   \n\n"); headers != nil {
		t.Fatalf("空输入应返回 nil, 实际 %#v", headers)
	}
}

// 自定义头必须能覆盖预设身份头，否则用户没法把 User-Agent 换成自己的。
func TestCustomHeadersOverrideIdentityHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	cfg := Config{SystemPromptPresetID: "codex", CustomHeaders: "User-Agent: my-agent/1.0\nx-extra: 1"}
	applyClientHeaders(req, "openai", cfg)
	applyCustomHeaders(req, cfg)
	if got := req.Header.Get("User-Agent"); got != "my-agent/1.0" {
		t.Fatalf("User-Agent 未被覆盖: %q", got)
	}
	if got := req.Header.Get("x-extra"); got != "1" {
		t.Fatalf("自定义头缺失: %q", got)
	}
}

// 自定义头要同时进入真实请求和预览，否则用户看到的 curl 与实际发送不一致。
func TestCustomHeadersAppearInPreview(t *testing.T) {
	cfg := Config{
		BaseURL:       "https://example.com/v1",
		APIKey:        "k",
		Protocol:      ProtocolOpenAIChat,
		Prompt:        "ping",
		CustomHeaders: "x-gateway-token: abc123",
	}
	text, err := Preview(cfg, TaskSpec{Root: "https://example.com/v1", Protocol: ProtocolOpenAIChat, Model: "m"}, false, PreviewCurl)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	// http.Header 会把头名规范成 X-Gateway-Token，断言不区分大小写。
	if !strings.Contains(strings.ToLower(text), "x-gateway-token: abc123") {
		t.Fatalf("预览未带自定义头:\n%s", text)
	}
}

func TestCustomHeadersSentOnProbe(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("x-gateway-token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:       srv.URL,
		APIKey:        "k",
		Protocol:      ProtocolOpenAIChat,
		Mode:          ModeNonStream,
		Prompt:        "ping",
		CustomHeaders: "x-gateway-token: abc123",
	}
	report, err := CheckModels(t.Context(), cfg, []TaskSpec{{Root: srv.URL, Protocol: ProtocolOpenAIChat, Model: "m"}})
	if err != nil {
		t.Fatalf("CheckModels: %v", err)
	}
	if seen != "abc123" {
		t.Fatalf("服务端未收到自定义头: %q", seen)
	}
	if !report.Results[0].NonStream.OK {
		t.Fatalf("探测应成功: %#v", report.Results[0].NonStream)
	}
}
