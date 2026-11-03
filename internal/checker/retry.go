package checker

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// acquirePermit 阻塞到拿到一个并发许可，或者 ctx 结束。
func acquirePermit(permits *atomic.Int64) {
	for {
		if permits.Add(-1) >= 0 {
			return
		}
		permits.Add(1)
		time.Sleep(20 * time.Millisecond)
	}
}

// halvePermits 在遇到限流时收缩并发窗口，下限为 1。
func halvePermits(permits *atomic.Int64, ceiling int) {
	next := permits.Load() / 2
	if next < 1 {
		next = 1
	}
	if int(next) > ceiling {
		next = int64(ceiling)
	}
	permits.Store(next)
}

// runTaskWithRetry 在可重试错误（限流 / 5xx / 网络抖动）上重试。
// 返回值 throttled 表示这次是否撞上了限流，供自适应并发收缩窗口。
func runTaskWithRetry(ctx context.Context, nonStreamClient, streamClient *http.Client, cfg Config, task modelTask) (ModelResult, bool) {
	attempts := cfg.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var last ModelResult
	throttled := false
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			break
		}
		last = runTask(ctx, nonStreamClient, streamClient, cfg, task)
		if !resultNeedsRetry(last) {
			return last, throttled
		}
		if hasErrorClass(last, "rate_limited") {
			throttled = true
		}
		if attempt == attempts-1 {
			break
		}
		// 退避：1s、2s、4s…上限 8s，避免把限流窗口撞穿。
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		if backoff > 8*time.Second {
			backoff = 8 * time.Second
		}
		// 上游明确要求等多久时以它为准（很多网关会回 Retry-After 或
		// retryAfterSeconds），上限 30s 防止异常值把检测挂死。
		if suggested := retryAfterDelayOf(last); suggested > backoff {
			backoff = suggested
			if backoff > maxRetryBackoff {
				backoff = maxRetryBackoff
			}
		}
		select {
		case <-ctx.Done():
			return last, throttled
		case <-time.After(backoff):
		}
	}
	return last, throttled
}

// resultNeedsRetry 判断一次模型探测是否值得重试。
func resultNeedsRetry(result ModelResult) bool {
	retryable := func(probe ProbeResult) bool {
		if !probe.Tested || probe.OK {
			return false
		}
		switch probe.ErrorClass {
		case "rate_limited", "server_error", "request_failed", "stream_first_timeout", "stream_idle_timeout", "stream_max_duration":
			return true
		}
		return probe.HTTPStatus >= 500
	}
	return retryable(result.NonStream) || retryable(result.Stream)
}

// maxRetryBackoff 是上游建议等待时长与指数退避的上限，避免异常值把检测挂死。
const maxRetryBackoff = 30 * time.Second

// retryAfterDelayOf 取一次模型探测里上游建议的最长等待时间。
func retryAfterDelayOf(result ModelResult) time.Duration {
	longest := result.NonStream.RetryAfterMS
	if result.Stream.RetryAfterMS > longest {
		longest = result.Stream.RetryAfterMS
	}
	if longest <= 0 {
		return 0
	}
	return time.Duration(longest) * time.Millisecond
}

// retryAfterDelay 从响应头 Retry-After（秒数或 HTTP 日期）和响应体里的
// retryAfterSeconds / retry_after 中解析上游建议的等待时长，单位毫秒。
// 解析不出来时返回 0，调用方继续用指数退避。
func retryAfterDelay(header http.Header, body []byte) int64 {
	if header != nil {
		if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
				return int64(seconds * 1000)
			}
			if when, err := http.ParseTime(value); err == nil {
				if wait := time.Until(when); wait > 0 {
					return wait.Milliseconds()
				}
			}
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}
	for _, key := range []string{"retryAfterSeconds", "retry_after_seconds", "retry_after"} {
		if value, ok := payload[key]; ok {
			if ms := secondsValueToMS(value); ms > 0 {
				return ms
			}
		}
	}
	return 0
}

// secondsValueToMS 把 JSON 里的秒数（可能是数字也可能是字符串）转成毫秒。
func secondsValueToMS(value any) int64 {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return int64(typed * 1000)
		}
	case string:
		if seconds, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil && seconds > 0 {
			return int64(seconds * 1000)
		}
	}
	return 0
}

// hasErrorClass 判断一次探测结果里是否出现了指定错误类别。
func hasErrorClass(result ModelResult, class string) bool {
	return result.NonStream.ErrorClass == class || result.Stream.ErrorClass == class
}
