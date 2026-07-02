package checker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunWithCompatibleServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Header.Get("Authorization") == "Bearer test-key" || r.Header.Get("x-api-key") == "test-key" {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"data":[{"id":"gpt-test"},{"id":"claude-test"}]}`)
				return
			}
			http.Error(w, `{"error":{"message":"bad key","type":"auth_failed"}}`, http.StatusUnauthorized)
		case "/v1/chat/completions":
			if !strings.Contains(r.Header.Get("Authorization"), "test-key") {
				http.Error(w, `{"error":{"message":"bad key","type":"auth_failed"}}`, http.StatusUnauthorized)
				return
			}
			writeChatResponse(w, r)
		case "/v1/responses":
			writeResponsesResponse(w, r)
		case "/v1/messages":
			if r.Header.Get("x-api-key") != "test-key" {
				http.Error(w, `{"error":{"message":"bad key","type":"auth_failed"}}`, http.StatusUnauthorized)
				return
			}
			writeAnthropicResponse(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := Run(context.Background(), Config{
		BaseURL:     server.URL + "/v1",
		APIKey:      "test-key",
		Protocol:    ProtocolAuto,
		Mode:        ModeBoth,
		Concurrency: 3,
		Timeout:     5 * time.Second,
		MaxModels:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(report.Sources))
	}
	if len(report.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(report.Results))
	}
	for _, result := range report.Results {
		if !result.NonStream.OK {
			t.Fatalf("nonstream failed for %s/%s: %+v", result.Protocol, result.Model, result.NonStream)
		}
		if !result.Stream.OK {
			t.Fatalf("stream failed for %s/%s: %+v", result.Protocol, result.Model, result.Stream)
		}
		if result.Stream.FirstTextMS <= 0 {
			t.Fatalf("missing first text latency for %s/%s: %+v", result.Protocol, result.Model, result.Stream)
		}
	}
}

func TestRunPreservesJSONErrorOnStreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"broken-stream"}]}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"model cannot stream","type":"unsupported_stream"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := Run(context.Background(), Config{
		BaseURL:  server.URL + "/v1",
		APIKey:   "test-key",
		Protocol: ProtocolOpenAIChat,
		Mode:     ModeStream,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(report.Results))
	}
	stream := report.Results[0].Stream
	if stream.OK {
		t.Fatalf("stream unexpectedly succeeded: %+v", stream)
	}
	if stream.ErrorClass != "unsupported_stream" {
		t.Fatalf("error class = %q, want unsupported_stream: %+v", stream.ErrorClass, stream)
	}
	if !strings.Contains(stream.ErrorMessage, "model cannot stream") {
		t.Fatalf("missing error message in stream result: %+v", stream)
	}
}

func TestCheckModelsReturnsFullRawResponseAndExtractedBody(t *testing.T) {
	longText := "pong-" + strings.Repeat("x", 640)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}],"debug":%q}`, longText, strings.Repeat("raw-", 220))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := CheckModels(context.Background(), Config{
		BaseURL:  server.URL + "/v1",
		APIKey:   "test-key",
		Protocol: ProtocolOpenAIChat,
		Mode:     ModeNonStream,
		Timeout:  5 * time.Second,
	}, []TaskSpec{{Root: server.URL + "/v1", Protocol: ProtocolOpenAIChat, Model: "gpt-long"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(report.Results))
	}
	nonstream := report.Results[0].NonStream
	if nonstream.Preview != longText {
		t.Fatalf("preview body was not extracted fully: got len %d want len %d", len(nonstream.Preview), len(longText))
	}
	if !strings.Contains(nonstream.RawResponse, strings.Repeat("raw-", 50)) {
		t.Fatalf("raw response missing full diagnostic payload: len=%d", len(nonstream.RawResponse))
	}
	if strings.Contains(nonstream.Preview, "...") || strings.HasSuffix(nonstream.RawResponse, "...") {
		t.Fatalf("response was truncated: preview=%q raw_suffix=%q", nonstream.Preview, tail(nonstream.RawResponse, 16))
	}
}

func TestCheckModelsEmitsStreamProgressDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var deltas []string
	report, err := CheckModels(context.Background(), Config{
		BaseURL:  server.URL + "/v1",
		APIKey:   "test-key",
		Protocol: ProtocolOpenAIChat,
		Mode:     ModeStream,
		Timeout:  5 * time.Second,
		Progress: func(event ProgressEvent) {
			deltas = append(deltas, event.Text)
		},
	}, []TaskSpec{{Root: server.URL + "/v1", Protocol: ProtocolOpenAIChat, Model: "gpt-stream"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(deltas, ""); got != "hello world" {
		t.Fatalf("stream deltas = %q, want hello world", got)
	}
	if got := report.Results[0].Stream.Preview; got != "hello world" {
		t.Fatalf("stream preview = %q, want hello world", got)
	}
}

func TestRequestsUseClientUserAgentByProtocolAndSurface(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, fmt.Sprintf(
			"%s ua=%s originator=%s platform=%s",
			r.URL.Path,
			r.Header.Get("User-Agent"),
			r.Header.Get("originator"),
			r.Header.Get("anthropic-client-platform"),
		))
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"ua-model"}]}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"pong"}}]}`)
		case "/v1/messages":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"content":[{"type":"text","text":"pong"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if _, err := ListModels(context.Background(), Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Protocol: ProtocolOpenAIChat, Timeout: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckModels(context.Background(), Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Protocol: ProtocolOpenAIChat, Mode: ModeNonStream, Timeout: 5 * time.Second, OpenAIClientSurface: ClientSurfaceDesktop}, []TaskSpec{{Root: server.URL + "/v1", Protocol: ProtocolOpenAIChat, Model: "ua-model"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckModels(context.Background(), Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Protocol: ProtocolAnthropicMessages, Mode: ModeNonStream, Timeout: 5 * time.Second, AnthropicClientSurface: ClientSurfaceCLI}, []TaskSpec{{Root: server.URL + "/v1", Protocol: ProtocolAnthropicMessages, Model: "ua-model"}}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(seen, "\n")
	for _, want := range []string{
		"/v1/models ua=codex_cli_rs/",
		"/v1/models ua=codex_cli_rs/" + codexClientVersion,
		"originator=codex_cli_rs",
		"/v1/chat/completions ua=codex_chatgpt_desktop/",
		"originator=codex_chatgpt_desktop",
		"/v1/messages ua=claude-code/" + claudeCodeClientVersion,
		"platform=claude_code_cli",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing client header %q in:\n%s", want, joined)
		}
	}
}

func writeChatResponse(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(readBody(tDiscard{}, r), `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"choices":[{"message":{"content":"pong"}}]}`)
}

func writeResponsesResponse(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(readBody(tDiscard{}, r), `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"output_text":"pong"}`)
}

func writeAnthropicResponse(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(readBody(tDiscard{}, r), `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"pong\"}}\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"content":[{"type":"text","text":"pong"}]}`)
}

type tDiscard struct{}

func readBody(_ tDiscard, r *http.Request) string {
	body, _ := io.ReadAll(r.Body)
	return string(body)
}

func tail(value string, size int) string {
	if len(value) <= size {
		return value
	}
	return value[len(value)-size:]
}
