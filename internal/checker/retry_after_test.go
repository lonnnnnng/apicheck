package checker

import (
	"net/http"
	"testing"
	"time"
)

// 上游明确给出等待时长时，重试退避要尊重它，而不是死守 1s/2s/4s。
func TestRetryAfterDelayParsesHeaderSeconds(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "7")
	if got := retryAfterDelay(header, nil); got != 7000 {
		t.Fatalf("Retry-After: 7 期望 7000ms, 实际 %d", got)
	}
}

func TestRetryAfterDelayParsesHTTPDate(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", time.Now().Add(3*time.Second).UTC().Format(http.TimeFormat))
	got := retryAfterDelay(header, nil)
	if got < 1000 || got > 4000 {
		t.Fatalf("HTTP 日期形式期望 1000~4000ms, 实际 %d", got)
	}
}

func TestRetryAfterDelayParsesBodySeconds(t *testing.T) {
	cases := []string{
		`{"retryAfterSeconds":5}`,
		`{"retry_after_seconds":5}`,
		`{"retry_after":"5"}`,
	}
	for _, body := range cases {
		if got := retryAfterDelay(nil, []byte(body)); got != 5000 {
			t.Fatalf("%s 期望 5000ms, 实际 %d", body, got)
		}
	}
}

func TestRetryAfterDelayIgnoresUnparseable(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "soon")
	if got := retryAfterDelay(header, []byte("not json")); got != 0 {
		t.Fatalf("无法解析时期望 0, 实际 %d", got)
	}
}

func TestRetryAfterDelayOfPicksLongestProbe(t *testing.T) {
	result := ModelResult{
		NonStream: ProbeResult{RetryAfterMS: 1500},
		Stream:    ProbeResult{RetryAfterMS: 4000},
	}
	if got := retryAfterDelayOf(result); got != 4*time.Second {
		t.Fatalf("期望 4s, 实际 %v", got)
	}
	if got := retryAfterDelayOf(ModelResult{}); got != 0 {
		t.Fatalf("没有建议时期望 0, 实际 %v", got)
	}
}
