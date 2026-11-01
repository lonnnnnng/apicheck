package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// 限流后重试：第一次 429，第二次 200，最终应判为可用。
func TestRetryRecoversFrom429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, APIKey: "k", Protocol: ProtocolOpenAIChat, Mode: ModeNonStream, Prompt: "ping", Retries: 1}
	report, err := CheckModels(context.Background(), cfg, []TaskSpec{{Root: srv.URL, Protocol: ProtocolOpenAIChat, Model: "m"}})
	if err != nil {
		t.Fatalf("CheckModels: %v", err)
	}
	if !report.Results[0].NonStream.OK {
		t.Fatalf("重试后应成功: %#v", report.Results[0].NonStream)
	}
	if calls.Load() != 2 {
		t.Fatalf("期望调用 2 次, 实际 %d", calls.Load())
	}
}

// 认证失败不该重试，否则只是白白拖慢整批。
func TestRetrySkipsAuthFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, APIKey: "k", Protocol: ProtocolOpenAIChat, Mode: ModeNonStream, Prompt: "ping", Retries: 3}
	report, err := CheckModels(context.Background(), cfg, []TaskSpec{{Root: srv.URL, Protocol: ProtocolOpenAIChat, Model: "m"}})
	if err != nil {
		t.Fatalf("CheckModels: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("401 不应重试, 实际调用 %d 次", calls.Load())
	}
	if report.Results[0].NonStream.ErrorClass != "auth_failed" {
		t.Fatalf("错误类别异常: %s", report.Results[0].NonStream.ErrorClass)
	}
}

func TestResultNeedsRetry(t *testing.T) {
	cases := []struct {
		name   string
		result ModelResult
		want   bool
	}{
		{"success", ModelResult{NonStream: ProbeResult{Tested: true, OK: true}}, false},
		{"rate limited", ModelResult{NonStream: ProbeResult{Tested: true, ErrorClass: "rate_limited"}}, true},
		{"server error", ModelResult{NonStream: ProbeResult{Tested: true, ErrorClass: "server_error"}}, true},
		{"http 503", ModelResult{NonStream: ProbeResult{Tested: true, HTTPStatus: 503}}, true},
		{"auth failed", ModelResult{NonStream: ProbeResult{Tested: true, ErrorClass: "auth_failed"}}, false},
		{"invalid request", ModelResult{NonStream: ProbeResult{Tested: true, ErrorClass: "invalid_request"}}, false},
		{"stream timeout", ModelResult{Stream: ProbeResult{Tested: true, ErrorClass: "stream_idle_timeout"}}, true},
		{"untested", ModelResult{}, false},
	}
	for _, testCase := range cases {
		if got := resultNeedsRetry(testCase.result); got != testCase.want {
			t.Fatalf("%s: 期望 %v, 实际 %v", testCase.name, testCase.want, got)
		}
	}
}

// 自适应并发：限流时窗口减半，成功时缓慢恢复且不超过上限。
func TestPermitWindowShrinksAndRecovers(t *testing.T) {
	var window atomic.Int64
	window.Store(8)
	halvePermits(&window, 8)
	if got := window.Load(); got != 4 {
		t.Fatalf("减半后期望 4, 实际 %d", got)
	}
	halvePermits(&window, 8)
	halvePermits(&window, 8)
	halvePermits(&window, 8)
	if got := window.Load(); got != 1 {
		t.Fatalf("下限应为 1, 实际 %d", got)
	}
	for i := 0; i < 20; i++ {
		window.Add(1)
		if window.Load() > 8 {
			window.Store(8)
		}
	}
	if got := window.Load(); got != 8 {
		t.Fatalf("恢复上限应为 8, 实际 %d", got)
	}
}

// 自适应并发打开时，整批任务仍然全部完成（许可只是限流，不会丢任务）。
func TestAdaptiveConcurrencyCompletesAllTasks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, APIKey: "k", Protocol: ProtocolOpenAIChat, Mode: ModeNonStream, Concurrency: 4, AdaptiveConcurrency: true}
	tasks := make([]TaskSpec, 0, 8)
	for i := 0; i < 8; i++ {
		tasks = append(tasks, TaskSpec{Root: srv.URL, Protocol: ProtocolOpenAIChat, Model: "m" + string(rune('a'+i))})
	}
	report, err := CheckModels(context.Background(), cfg, tasks)
	if err != nil {
		t.Fatalf("CheckModels: %v", err)
	}
	if len(report.Results) != 8 {
		t.Fatalf("期望 8 条结果, 实际 %d", len(report.Results))
	}
	for _, result := range report.Results {
		if !result.NonStream.OK {
			t.Fatalf("全部任务都应成功: %#v", result.NonStream)
		}
	}
}
