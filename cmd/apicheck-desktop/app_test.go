package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"apicheck/internal/checker"
)

func TestAppCheck(t *testing.T) {
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
			if !strings.Contains(r.Header.Get("Authorization"), "test-key") {
				http.Error(w, `{"error":{"message":"bad key","type":"auth_failed"}}`, http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"pong"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	app := NewApp()
	report, err := app.Check(CheckRequest{
		BaseURL:     provider.URL + "/v1",
		APIKey:      "test-key",
		Protocol:    "openai-chat",
		Mode:        "nonstream",
		Concurrency: 1,
		Timeout:     "5s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(report.Results))
	}
	if !report.Results[0].NonStream.OK {
		t.Fatalf("desktop app check failed: %+v", report.Results[0].NonStream)
	}
}

func TestAppListModelsThenCheckModels(t *testing.T) {
	var seenUserAgents []string
	var seenOriginators []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUserAgents = append(seenUserAgents, r.Header.Get("User-Agent"))
		seenOriginators = append(seenOriginators, r.Header.Get("originator"))
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"gpt-desktop"}]}`)
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "desktop custom prompt") {
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

	app := NewApp()
	listReport, err := app.ListModels(CheckRequest{
		BaseURL:  provider.URL + "/v1",
		APIKey:   "test-key",
		Protocol: "openai-chat",
		Mode:     "nonstream",
		Timeout:  "5s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listReport.Tasks) != 1 || len(listReport.Results) != 0 {
		t.Fatalf("unexpected list report: %+v", listReport)
	}

	checkReport, err := app.CheckModels(CheckRequest{
		BaseURL:  provider.URL + "/v1",
		APIKey:   "test-key",
		Prompt:   "desktop custom prompt",
		Protocol: "openai-chat",
		Mode:     "nonstream",
		Timeout:  "5s",
		Tasks:    listReport.Tasks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(checkReport.Results) != 1 || !checkReport.Results[0].NonStream.OK {
		t.Fatalf("desktop split check failed: %+v", checkReport)
	}
	joined := strings.Join(seenUserAgents, "\n")
	if !strings.Contains(joined, "codex_chatgpt_desktop/") {
		t.Fatalf("desktop requests did not use Codex desktop User-Agent: %q", joined)
	}
	if !strings.Contains(strings.Join(seenOriginators, "\n"), "codex_chatgpt_desktop") {
		t.Fatalf("desktop requests did not use Codex desktop originator: %q", seenOriginators)
	}
}

func TestAppHonorsConfiguredUASurfaces(t *testing.T) {
	var seen []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, fmt.Sprintf(
			"%s ua=%s originator=%s platform=%s",
			r.URL.Path,
			r.Header.Get("User-Agent"),
			r.Header.Get("originator"),
			r.Header.Get("anthropic-client-platform"),
		))
		switch r.URL.Path {
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
	defer provider.Close()

	app := NewApp()
	_, err := app.CheckModels(CheckRequest{
		BaseURL:         provider.URL + "/v1",
		APIKey:          "test-key",
		Protocol:        "openai-chat",
		Mode:            "nonstream",
		Timeout:         "5s",
		OpenAIUASurface: "cli",
		Tasks:           []checker.TaskSpec{{Root: provider.URL + "/v1", Protocol: checker.ProtocolOpenAIChat, Model: "gpt-cli"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.CheckModels(CheckRequest{
		BaseURL:            provider.URL + "/v1",
		APIKey:             "test-key",
		Protocol:           "anthropic-messages",
		Mode:               "nonstream",
		Timeout:            "5s",
		AnthropicUASurface: "cli",
		Tasks:              []checker.TaskSpec{{Root: provider.URL + "/v1", Protocol: checker.ProtocolAnthropicMessages, Model: "claude-cli"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(seen, "\n")
	for _, want := range []string{
		"/v1/chat/completions ua=codex_cli_rs/",
		"originator=codex_cli_rs",
		"/v1/messages ua=claude-code/",
		"platform=claude_code_cli",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing configured UA surface %q in:\n%s", want, joined)
		}
	}
}

func TestDesktopConfigHonorsStreamTimeouts(t *testing.T) {
	cfg := configFromRequest(CheckRequest{
		Timeout:            "9s",
		ConnectTimeout:     "30s",
		StreamFirstTimeout: "11s",
		StreamIdleTimeout:  "12s",
		StreamMaxDuration:  "0",
	})
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
