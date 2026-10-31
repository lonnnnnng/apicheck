package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 多账号共享同一 base_url 与模型时，账号维度必须保留：否则去重会把两个账号的请求合并成一次，
// 前端也只能把结果归到一个账号上。
func TestTasksKeepAccountIsolation(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","choices":[{"message":{"role":"assistant","content":"pong"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()

	report, err := CheckModels(context.Background(), Config{BaseURL: server.URL, APIKey: "k", Mode: ModeNonStream, Concurrency: 1}, []TaskSpec{
		{AccountID: "acct-a", Root: server.URL, Protocol: ProtocolOpenAIChat, Model: "gpt-4o"},
		{AccountID: "acct-b", Root: server.URL, Protocol: ProtocolOpenAIChat, Model: "gpt-4o"},
	})
	if err != nil {
		t.Fatalf("CheckModels: %v", err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("want 2 results (one per account), got %d: %+v", len(report.Results), report.Results)
	}
	if hits != 2 {
		t.Fatalf("want 2 upstream calls, got %d", hits)
	}
	seen := map[string]bool{}
	for _, result := range report.Results {
		seen[result.AccountID] = true
	}
	if !seen["acct-a"] || !seen["acct-b"] {
		t.Fatalf("account_id not passed through: %+v", report.Results)
	}
	if report.Results[0].AccountID != "acct-a" || report.Results[1].AccountID != "acct-b" {
		t.Fatalf("results should sort by account for stable output: %+v", report.Results)
	}
	payload, err := json.Marshal(report.Results[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !containsJSON(payload, `"account_id":"acct-a"`) {
		t.Fatalf("account_id missing from json: %s", payload)
	}
}

// 单账号模式下 account_id 为空时不能出现在 JSON 里，避免 CLI 与旧前端看到多余字段。
func TestTasksWithoutAccountOmitField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()
	report, err := CheckModels(context.Background(), Config{BaseURL: server.URL, APIKey: "k", Mode: ModeNonStream, Concurrency: 1}, []TaskSpec{
		{Root: server.URL, Protocol: ProtocolOpenAIChat, Model: "gpt-4o"},
	})
	if err != nil {
		t.Fatalf("CheckModels: %v", err)
	}
	payload, err := json.Marshal(report.Results[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if containsJSON(payload, "account_id") {
		t.Fatalf("empty account_id should be omitted: %s", payload)
	}
}

func containsJSON(payload []byte, needle string) bool {
	return strings.Contains(string(payload), needle)
}
