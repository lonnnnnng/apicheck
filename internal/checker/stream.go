package checker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func readNonStreamResult(resp *http.Response, protocol Protocol, start time.Time, result *ProbeResult) {
	// 作者 long: 恶意或异常网关可能返回无限长的响应体，这里读到上限即止，
	// 截断标记拼接在原始响应末尾，便于用户识别。
	limited := io.LimitReader(resp.Body, nonStreamResponseLimitBytes+1)
	body, readErr := io.ReadAll(limited)
	truncated := len(body) > nonStreamResponseLimitBytes
	if truncated {
		body = body[:nonStreamResponseLimitBytes]
	} else if readErr != nil {
		result.TotalMS = elapsedMS(start)
		result.ErrorClass = "read_failed"
		result.ErrorMessage = readErr.Error()
		return
	}
	if truncated {
		body = append(body, streamTruncatedMarker...)
	}
	result.TotalMS = elapsedMS(start)
	result.RawResponse = trimBody(body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.ErrorClass = classifyHTTPError(resp.StatusCode, body)
		result.ErrorMessage = sanitizeErrorMessage(extractErrorMessage(body))
		result.Preview = result.ErrorMessage
		result.RawError = result.RawResponse
		result.RetryAfterMS = retryAfterDelay(resp.Header, body)
		return
	}
	if text := extractConversationText(protocol, body); text != "" {
		result.Preview = text
	} else {
		result.Preview = result.RawResponse
	}
	result.TokenUsage = extractTokenUsage(protocol, body, false)
	if reason, truncated := extractResponseTruncation(protocol, body); truncated {
		result.Truncated = true
		result.IncompleteReason = reason
	}
	result.OK = true
}

// extractResponseTruncation 识别非流式响应里的"被 max tokens 截断"标记。
func extractResponseTruncation(protocol Protocol, body []byte) (string, bool) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "", false
	}
	switch protocol {
	case ProtocolOpenAIResponses:
		if stringField(payload, "status") == "incomplete" {
			details, _ := payload["incomplete_details"].(map[string]any)
			return firstNonEmptyString(stringField(details, "reason"), "incomplete"), true
		}
	case ProtocolAnthropicMessages:
		if stringField(payload, "stop_reason") == "max_tokens" {
			return "max_tokens", true
		}
	case ProtocolOpenAIChat:
		for _, choice := range asSlice(payload["choices"]) {
			item, _ := choice.(map[string]any)
			if stringField(item, "finish_reason") == "length" {
				return "max_tokens", true
			}
		}
	}
	return "", false
}

type streamReadItem struct {
	line string
	err  error
}

// streamCapture 跟踪流式探测的原始响应与文本预览，并封顶累积体积：
// 达上限后丢弃后续写入但置 truncated 标志，终结时在末尾补截断标记。
type streamCapture struct {
	raw       strings.Builder
	text      strings.Builder
	rawDone   bool
	textDone  bool
	rawTrunc  bool
	textTrunc bool
	// 流式响应可能以 HTTP 200 开始，最后用 response.failed / error 事件宣告失败。
	// 这种失败不能当成成功，所以单独记录事件里的错误码与错误信息。
	errSeen bool
	errCode string
	errMsg  string
	// 上游明确告知"被 max_output_tokens 截断"（responses 的 response.incomplete /
	// anthropic 的 stop_reason=max_tokens）。这类响应链路是通的，不算失败。
	truncSeen   bool
	truncReason string
}

func (c *streamCapture) recordEventError(code, message string) {
	if code == "" && message == "" {
		return
	}
	if message != "" {
		c.errMsg = message
	}
	if code != "" {
		c.errCode = code
	}
	c.errSeen = true
}

func (c *streamCapture) eventError() (string, string, bool) {
	return c.errCode, c.errMsg, c.errSeen
}

func (c *streamCapture) recordTruncation(reason string) {
	c.truncSeen = true
	if reason != "" {
		c.truncReason = reason
	}
}

func (c *streamCapture) truncation() (string, bool) {
	return c.truncReason, c.truncSeen
}

func (c *streamCapture) writeRawLine(line string) {
	if c.rawDone {
		c.rawTrunc = true
		return
	}
	if c.raw.Len()+len(line)+1 > streamRawLimitBytes {
		c.rawDone = true
		c.rawTrunc = true
		return
	}
	c.raw.WriteString(line)
	c.raw.WriteByte('\n')
}

func (c *streamCapture) writeText(text string) {
	if c.textDone {
		if text != "" {
			c.textTrunc = true
		}
		return
	}
	if c.text.Len()+len(text) > streamTextLimitBytes {
		c.textDone = true
		if text != "" {
			c.textTrunc = true
		}
		return
	}
	c.text.WriteString(text)
}

func (c *streamCapture) writeTextLine(line string) {
	if line == "" {
		return
	}
	if c.textDone {
		c.textTrunc = true
		return
	}
	if c.text.Len()+len(line)+1 > streamTextLimitBytes {
		c.textDone = true
		c.textTrunc = true
		return
	}
	c.text.WriteString(line)
	c.text.WriteByte('\n')
}

func (c *streamCapture) rawString() string {
	if c.rawTrunc {
		return c.raw.String() + streamTruncatedMarker + "\n"
	}
	return c.raw.String()
}

func (c *streamCapture) textString() string {
	if c.textTrunc {
		return c.text.String() + streamTruncatedMarker + "\n"
	}
	return c.text.String()
}

func readStreamResult(resp *http.Response, task modelTask, start time.Time, cfg Config, result *ProbeResult) {
	lines := make(chan streamReadItem, 8)
	done := make(chan struct{})
	defer close(done)
	go scanStreamLines(resp.Body, lines, done)

	capture := &streamCapture{}
	var eventData []string

	firstTimer := time.NewTimer(cfg.StreamFirstTimeout)
	defer firstTimer.Stop()

	var idleTimer *time.Timer
	var idleC <-chan time.Time
	if cfg.StreamIdleTimeout > 0 {
		idleTimer = time.NewTimer(cfg.StreamIdleTimeout)
		if !idleTimer.Stop() {
			<-idleTimer.C
		}
		idleC = idleTimer.C
		defer idleTimer.Stop()
	}

	var maxTimer *time.Timer
	var maxC <-chan time.Time
	if cfg.StreamMaxDuration > 0 {
		remaining := cfg.StreamMaxDuration - time.Since(start)
		if remaining <= 0 {
			setStreamTimeoutResult(resp, result, capture, start, "stream_max_duration", cfg.StreamMaxDuration)
			return
		}
		maxTimer = time.NewTimer(remaining)
		maxC = maxTimer.C
		defer maxTimer.Stop()
	}

	firstPacketSeen := false
	for {
		select {
		case item, ok := <-lines:
			if !ok {
				finalizeStreamResult(resp, task, start, capture, &eventData, result, cfg.Progress)
				return
			}
			if item.err != nil {
				handleSSEEvent(task, start, capture, &eventData, result, cfg.Progress)
				result.TotalMS = elapsedMS(start)
				result.RawResponse = trimBody([]byte(capture.rawString()))
				result.ErrorClass = "stream_read_failed"
				result.ErrorMessage = item.err.Error()
				return
			}
			if !firstPacketSeen {
				firstPacketSeen = true
				stopTimer(firstTimer)
			}
			resetTimer(idleTimer, cfg.StreamIdleTimeout)
			processStreamLine(resp, task, start, item.line, capture, &eventData, result, cfg.Progress)
		case <-firstTimer.C:
			setStreamTimeoutResult(resp, result, capture, start, "stream_first_timeout", cfg.StreamFirstTimeout)
			return
		case <-idleC:
			setStreamTimeoutResult(resp, result, capture, start, "stream_idle_timeout", cfg.StreamIdleTimeout)
			return
		case <-maxC:
			setStreamTimeoutResult(resp, result, capture, start, "stream_max_duration", cfg.StreamMaxDuration)
			return
		}
	}
}

func scanStreamLines(body io.ReadCloser, out chan<- streamReadItem, done <-chan struct{}) {
	defer close(out)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if !sendStreamReadItem(done, out, streamReadItem{line: strings.TrimRight(scanner.Text(), "\r")}) {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		sendStreamReadItem(done, out, streamReadItem{err: err})
	}
}

func sendStreamReadItem(done <-chan struct{}, out chan<- streamReadItem, item streamReadItem) bool {
	// 作者 long: 超时路径会主动关闭响应体并返回结果，读取协程需要能立刻退出，
	// 否则慢速网关持续写入时可能卡在无人接收的 channel 发送上。
	select {
	case <-done:
		return false
	default:
	}
	select {
	case <-done:
		return false
	case out <- item:
		return true
	}
}

func processStreamLine(resp *http.Response, task modelTask, start time.Time, line string, capture *streamCapture, eventData *[]string, result *ProbeResult, progress func(ProgressEvent)) {
	capture.writeRawLine(line)
	if line == "" {
		handleSSEEvent(task, start, capture, eventData, result, progress)
		*eventData = nil
		return
	}
	if strings.HasPrefix(line, "data:") {
		*eventData = append(*eventData, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 兼容网关在流式探测失败时常直接返回 JSON 错误，而不是 SSE。
		// 保留这些普通行，才能给用户看到真实的鉴权/模型/端点错误。
		capture.writeTextLine(line)
	}
}

func finalizeStreamResult(resp *http.Response, task modelTask, start time.Time, capture *streamCapture, eventData *[]string, result *ProbeResult, progress func(ProgressEvent)) {
	handleSSEEvent(task, start, capture, eventData, result, progress)
	result.TotalMS = elapsedMS(start)
	rawBody := []byte(capture.rawString())
	result.RawResponse = trimBody(rawBody)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.ErrorClass = classifyHTTPError(resp.StatusCode, rawBody)
		result.ErrorMessage = sanitizeErrorMessage(extractErrorMessage(rawBody))
		result.Preview = result.ErrorMessage
		result.RawError = result.RawResponse
		result.RetryAfterMS = retryAfterDelay(resp.Header, rawBody)
		return
	}
	// HTTP 200 + response.failed（或 anthropic error 事件）属于失败：
	// 网关先握手成功，再把上游错误塞进流里，这时不能报成功。
	if code, message, failed := capture.eventError(); failed {
		result.ErrorClass = classifyStreamEventError(code, message)
		result.ErrorMessage = sanitizeErrorMessage(message)
		if text := strings.TrimSpace(capture.textString()); text != "" {
			result.Preview = text
		} else {
			result.Preview = result.ErrorMessage
		}
		result.RawError = result.RawResponse
		return
	}
	if text := strings.TrimSpace(capture.textString()); text != "" {
		result.Preview = text
	} else if !looksLikeEventStream(capture.rawString()) {
		// 网关可能对 stream=true 直接回一段普通 JSON；这段内容当预览更有用。
		// 真正的 SSE 原始数据只留在 raw_response 里，避免整段事件流灌进预览区。
		result.Preview = result.RawResponse
	}
	if reason, truncated := capture.truncation(); truncated {
		result.Truncated = true
		result.IncompleteReason = reason
	}
	result.OK = true
}

func setStreamTimeoutResult(resp *http.Response, result *ProbeResult, capture *streamCapture, start time.Time, class string, duration time.Duration) {
	_ = resp.Body.Close()
	result.TotalMS = elapsedMS(start)
	result.RawResponse = trimBody([]byte(capture.rawString()))
	result.RawError = result.RawResponse
	if text := strings.TrimSpace(capture.textString()); text != "" {
		result.Preview = text
	}
	result.ErrorClass = class
	result.ErrorMessage = fmt.Sprintf("%s after %s", class, duration)
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if timer == nil || duration <= 0 {
		return
	}
	stopTimer(timer)
	timer.Reset(duration)
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func handleSSEEvent(task modelTask, start time.Time, capture *streamCapture, eventData *[]string, result *ProbeResult, progress func(ProgressEvent)) {
	if len(*eventData) == 0 {
		return
	}
	data := strings.Join(*eventData, "\n")
	if data == "[DONE]" {
		return
	}
	if text := extractConversationText(task.protocol, []byte(data)); text != "" {
		// responses 协议的 *.done 事件会把已经流式下发过的文本再完整带一遍，
		// 直接追加会出现 "pong"+"pong"="pongpong"，这里对已完成事件做去重。
		if !streamTextAlreadyCaptured(task.protocol, capture, []byte(data), text) {
			capture.writeText(text)
			emitProgress(progress, task, "delta", text, data)
		}
	}
	if result.FirstTextMS == 0 && eventHasText(task.protocol, []byte(data)) {
		result.FirstTextMS = elapsedMS(start)
	}
	// usage 只在最后一个事件里出现（chat 的最终 chunk / responses 的 completed /
	// anthropic 的 message_delta），每次覆盖写入即可拿到最终值。
	if usage := extractTokenUsage(task.protocol, []byte(data), true); usage != (TokenUsage{}) {
		result.TokenUsage = usage
	}
	if code, message, failed := extractStreamEventError([]byte(data)); failed {
		capture.recordEventError(code, message)
	}
	if reason, truncated := extractStreamTruncation(task.protocol, []byte(data)); truncated {
		capture.recordTruncation(reason)
	}
}

// streamTextAlreadyCaptured 判断本次事件携带的文本是否已经通过增量事件写入过。
// 只对 responses 协议的收尾事件（*.done / response.completed）做判断，
// 其它协议与增量事件原样返回 false，保持既有行为。
func streamTextAlreadyCaptured(protocol Protocol, capture *streamCapture, body []byte, text string) bool {
	if protocol != ProtocolOpenAIResponses || capture == nil || text == "" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	if !strings.HasSuffix(stringField(payload, "type"), ".done") && stringField(payload, "type") != "response.completed" {
		return false
	}
	captured := capture.text.String()
	return captured != "" && strings.Contains(captured, text)
}

func emitProgress(progress func(ProgressEvent), task modelTask, kind, text, raw string) {
	if progress == nil {
		return
	}
	progress(ProgressEvent{
		Root:     task.root,
		Protocol: task.protocol,
		Model:    task.model,
		Mode:     ModeStream,
		Kind:     kind,
		Text:     text,
		Raw:      raw,
	})
}

func elapsedMS(start time.Time) int64 {
	return durationMS(time.Since(start))
}

func durationMS(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	ms := duration.Milliseconds()
	if ms == 0 {
		return 1
	}
	return ms
}
