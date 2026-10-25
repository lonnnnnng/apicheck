package checker

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractTokenUsageOpenAIChat(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"pong"}}],"usage":{"prompt_tokens":120,"completion_tokens":8,"total_tokens":128,"prompt_tokens_details":{"cached_tokens":64}}}`)
	usage := extractTokenUsage(ProtocolOpenAIChat, body, false)
	if usage.Input != 120 || usage.Output != 8 || usage.Total != 128 || usage.Cached != 64 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.FromStream {
		t.Fatal("non-stream usage should not be flagged")
	}
}

func TestExtractTokenUsageResponses(t *testing.T) {
	body := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5,"output_tokens_details":{"cached_tokens":0}}}}`)
	usage := extractTokenUsage(ProtocolOpenAIResponses, body, true)
	if usage.Input != 10 || usage.Output != 5 || usage.Total != 15 || usage.Cached != 0 || !usage.FromStream {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestExtractTokenUsageAnthropic(t *testing.T) {
	body := []byte(`{"type":"message_delta","usage":{"input_tokens":100,"output_tokens":9,"cache_read_input_tokens":80,"cache_creation_input_tokens":20}}`)
	usage := extractTokenUsage(ProtocolAnthropicMessages, body, true)
	if usage.Input != 100 || usage.Output != 9 || usage.Cached != 100 || usage.Total != 109 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestExtractTokenUsageMissing(t *testing.T) {
	if usage := extractTokenUsage(ProtocolOpenAIChat, []byte(`{"choices":[{"delta":{"content":"hi"}}]}`), true); usage != (TokenUsage{}) {
		t.Fatalf("expected zero usage, got %+v", usage)
	}
	if usage := extractTokenUsage(ProtocolOpenAIChat, []byte(`{"usage":{}}`), false); usage != (TokenUsage{}) {
		t.Fatalf("expected zero usage for empty usage, got %+v", usage)
	}
}

func TestNonStreamResultCarriesUsage(t *testing.T) {
	result := &ProbeResult{}
	body := `{"choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	readNonStreamResult(resp, ProtocolOpenAIChat, time.Now(), result)
	if !result.OK || result.TokenUsage.Input != 7 || result.TokenUsage.Output != 2 || result.TokenUsage.Total != 9 {
		t.Fatalf("result = ok:%v usage:%+v", result.OK, result.TokenUsage)
	}
}
