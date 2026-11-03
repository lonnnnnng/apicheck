package checker

import (
	"encoding/json"
	"net/http"
	"strings"
)

// extractTokenUsage 从响应体的 usage 字段提取 token 用量，覆盖三种协议：
//   - openai-chat:  usage.prompt_tokens / completion_tokens / total_tokens /
//     prompt_tokens_details.cached_tokens
//   - openai-responses: usage.input_tokens / output_tokens / total_tokens /
//     output_tokens_details.cached_tokens
//   - anthropic-messages: usage.input_tokens / output_tokens /
//     cache_read_input_tokens + cache_creation_input_tokens
//
// 网关普遍会省略 usage（尤其流式中间 chunk），此时返回零值让前端不展示。
// 流式场景下 chunk 可能不完整，解析失败时静默跳过。
func extractTokenUsage(protocol Protocol, body []byte, fromStream bool) TokenUsage {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return TokenUsage{}
	}
	usage, _ := payload["usage"].(map[string]any)
	if usage == nil {
		// OpenAI Responses 把 usage 包在 response 对象里。
		if response, ok := payload["response"].(map[string]any); ok {
			usage, _ = response["usage"].(map[string]any)
		}
	}
	if usage == nil {
		return TokenUsage{}
	}
	numeric := func(keys ...string) int64 {
		for _, key := range keys {
			if value, ok := usage[key].(float64); ok && value > 0 {
				return int64(value)
			}
		}
		return 0
	}
	cachedIn := func(container map[string]any, keys ...string) int64 {
		for _, key := range keys {
			if value, ok := container[key].(float64); ok && value > 0 {
				return int64(value)
			}
		}
		return 0
	}
	token := TokenUsage{FromStream: fromStream}
	switch protocol {
	case ProtocolOpenAIChat:
		token.Input = numeric("prompt_tokens")
		token.Output = numeric("completion_tokens")
		token.Total = numeric("total_tokens")
		if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
			token.Cached = cachedIn(details, "cached_tokens")
		}
	case ProtocolOpenAIResponses:
		token.Input = numeric("input_tokens")
		token.Output = numeric("output_tokens")
		token.Total = numeric("total_tokens")
		if details, ok := usage["output_tokens_details"].(map[string]any); ok {
			token.Cached = cachedIn(details, "cached_tokens")
		}
	case ProtocolAnthropicMessages:
		token.Input = numeric("input_tokens")
		token.Output = numeric("output_tokens")
		token.Cached = numeric("cache_read_input_tokens") + numeric("cache_creation_input_tokens")
	default:
		return TokenUsage{}
	}
	if token.Total == 0 && (token.Input > 0 || token.Output > 0) {
		token.Total = token.Input + token.Output
	}
	if token.Input == 0 && token.Output == 0 && token.Cached == 0 && token.Total == 0 {
		return TokenUsage{}
	}
	return token
}

func extractConversationText(protocol Protocol, body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return trimBody(body)
	}
	var parts []string
	switch protocol {
	case ProtocolOpenAIChat:
		for _, choice := range asSlice(payload["choices"]) {
			item, _ := choice.(map[string]any)
			appendContentText(&parts, item["message"])
			appendContentText(&parts, item["delta"])
			appendContentText(&parts, item["text"])
		}
	case ProtocolOpenAIResponses:
		appendContentText(&parts, payload["output_text"])
		appendContentText(&parts, payload["delta"])
		appendContentText(&parts, payload["text"])
		appendContentText(&parts, payload["content"])
		for _, output := range asSlice(payload["output"]) {
			appendContentText(&parts, output)
		}
	case ProtocolAnthropicMessages:
		appendContentText(&parts, payload["content"])
		appendContentText(&parts, payload["delta"])
	default:
		appendContentText(&parts, payload["content"])
		appendContentText(&parts, payload["text"])
	}
	return strings.Join(parts, "")
}

func appendContentText(parts *[]string, value any) {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			*parts = append(*parts, typed)
		}
	case []any:
		for _, item := range typed {
			appendContentText(parts, item)
		}
	case map[string]any:
		appendContentText(parts, typed["text"])
		appendContentText(parts, typed["output_text"])
		appendContentText(parts, typed["content"])
		appendContentText(parts, typed["message"])
		appendContentText(parts, typed["delta"])
	}
}

func asSlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func eventHasText(protocol Protocol, body []byte) bool {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return len(strings.TrimSpace(string(body))) > 0
	}
	switch protocol {
	case ProtocolOpenAIChat:
		return openAIChatChunkHasText(payload)
	case ProtocolOpenAIResponses:
		return stringField(payload, "delta") != "" || stringField(payload, "text") != ""
	case ProtocolAnthropicMessages:
		if delta, ok := payload["delta"].(map[string]any); ok && stringField(delta, "text") != "" {
			return true
		}
		if content, ok := payload["content"].(map[string]any); ok && stringField(content, "text") != "" {
			return true
		}
	}
	return false
}

func openAIChatChunkHasText(payload map[string]any) bool {
	choices, _ := payload["choices"].([]any)
	for _, choice := range choices {
		item, _ := choice.(map[string]any)
		delta, _ := item["delta"].(map[string]any)
		if stringField(delta, "content") != "" {
			return true
		}
	}
	return false
}

func stringField(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func classifyHTTPError(status int, body []byte) string {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "auth_failed"
	}
	if status == http.StatusNotFound {
		return "endpoint_not_found"
	}
	if status == http.StatusTooManyRequests {
		return "rate_limited"
	}
	if message := extractErrorCode(body); message != "" {
		return message
	}
	if status >= 500 {
		return "server_error"
	}
	if status >= 400 {
		return "invalid_request"
	}
	return "unknown_error"
}

func extractErrorCode(body []byte) string {
	var payload struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	if payload.Error.Code != "" {
		return payload.Error.Code
	}
	return payload.Error.Type
}

func extractErrorMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error.Message != "" {
		return payload.Error.Message
	}
	return compactPreview(body)
}

func compactPreview(body []byte) string {
	return strings.Join(strings.Fields(trimBody(body)), " ")
}

// extractStreamEventError 识别流式响应里的失败事件。
// 网关常见形态：
//   - openai-responses: {"type":"response.failed","response":{"error":{...}}}
//   - openai-responses: {"type":"error","error":{...}}
//   - anthropic-messages: {"type":"error","error":{"type":"overloaded_error","message":...}}
//   - openai-chat:      {"error":{"message":...}}
func extractStreamEventError(body []byte) (string, string, bool) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "", "", false
	}
	if code, message, ok := errorObjectFields(payload["error"]); ok {
		return code, message, true
	}
	switch stringField(payload, "type") {
	case "response.failed":
		response, _ := payload["response"].(map[string]any)
		if code, message, ok := errorObjectFields(response["error"]); ok {
			return code, message, true
		}
		return "", "", false
	}
	return "", "", false
}

// extractStreamTruncation 识别"输出被 max tokens 截断"的事件。
// 这种响应模型已经正常出字，说明链路是通的，不能按失败处理。
func extractStreamTruncation(protocol Protocol, body []byte) (string, bool) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "", false
	}
	switch stringField(payload, "type") {
	case "response.incomplete":
		response, _ := payload["response"].(map[string]any)
		details, _ := response["incomplete_details"].(map[string]any)
		reason := firstNonEmptyString(stringField(details, "reason"), "incomplete")
		return reason, true
	case "response.completed":
		// 少数网关把截断信息挂在 completed 事件上。
		response, _ := payload["response"].(map[string]any)
		details, _ := response["incomplete_details"].(map[string]any)
		if reason := stringField(details, "reason"); reason != "" {
			return reason, true
		}
	case "message_delta":
		if protocol != ProtocolAnthropicMessages {
			return "", false
		}
		delta, _ := payload["delta"].(map[string]any)
		if stringField(delta, "stop_reason") == "max_tokens" {
			return "max_tokens", true
		}
	case "message_stop":
		if protocol != ProtocolAnthropicMessages {
			return "", false
		}
	}
	return "", false
}

func errorObjectFields(value any) (string, string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", "", false
	}
	code := firstNonEmptyString(stringField(object, "code"), stringField(object, "type"), stringField(object, "status"))
	message := firstNonEmptyString(stringField(object, "message"), stringField(object, "detail"), stringField(object, "description"))
	if code == "" && message == "" {
		return "", "", false
	}
	return code, message, true
}

// classifyStreamEventError 把流式失败事件的错误码/错误信息归一成和 HTTP 错误一致的分类。
func classifyStreamEventError(code, message string) string {
	switch strings.TrimSpace(code) {
	case "400":
		return "invalid_request"
	case "401", "403":
		return "auth_failed"
	case "404":
		return "endpoint_not_found"
	case "429":
		return "rate_limited"
	case "500", "502", "503", "504":
		return "server_error"
	}
	lower := strings.ToLower(code + " " + message)
	switch {
	case containsAny(lower, "rate limit", "rate_limit", "too many requests", "quota", "overloaded", "排队", "限流", "资源不足", "resource_limit"):
		return "rate_limited"
	case containsAny(lower, "unauthorized", "invalid api key", "authentication", "permission"):
		return "auth_failed"
	case containsAny(lower, "model not found", "no such model", "not found"):
		return "endpoint_not_found"
	case containsAny(lower, "timeout", "timed out", "internal", "unavailable"):
		return "server_error"
	case containsAny(lower, "invalid", "unsupported", "bad request"):
		return "invalid_request"
	}
	if trimmed := strings.TrimSpace(code); trimmed != "" {
		return trimmed
	}
	return "stream_error"
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// looksLikeEventStream 判断原始响应是不是 SSE 事件流。
// 真正的 SSE 不该当作对话内容展示，只保留在原始数据里。
func looksLikeEventStream(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "event:") || strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "id:") || trimmed == ":"
	}
	return false
}

// errorMessageLimitRunes 限制错误信息长度，避免上游把整段 JSON 灌进界面。
const errorMessageLimitRunes = 1200

// sanitizeErrorMessage 把网关层层套娃的 JSON 错误信息压平成人类可读文本。
// 上游常见形态："上游排队中…上游详情：{\"code\":\"403\",\"message\":\"{\\\"code\\\":\\\"10605\\\"…}\"}"。
func sanitizeErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	head, codes, detail := flattenErrorMessage(message)
	parts := []string{}
	if head != "" {
		parts = append(parts, head)
	}
	if len(codes) > 0 {
		parts = append(parts, "上游错误码："+strings.Join(codes, " → "))
	}
	if detail != "" {
		label := "上游详情："
		if strings.HasSuffix(head, "：") || strings.HasSuffix(head, ":") {
			label = ""
		}
		parts = append(parts, label+detail)
	}
	out := strings.Join(parts, "\n")
	if strings.TrimSpace(out) == "" {
		out = message
	}
	return truncateErrorMessage(out)
}

// flattenErrorMessage 沿 message 字段剥洋葱：外层人读文本保留为 head，
// 内层错误码按层级收集，最内层可读信息作为 detail。
func flattenErrorMessage(message string) (head string, codes []string, detail string) {
	current := strings.TrimSpace(message)
	head = current
	for depth := 0; depth < 5; depth++ {
		index := strings.Index(current, "{")
		if index < 0 {
			break
		}
		var payload map[string]any
		if json.Unmarshal([]byte(current[index:]), &payload) != nil {
			break
		}
		code, inner, ok := errorObjectFields(payload)
		if !ok {
			break
		}
		if code != "" {
			codes = append(codes, code)
		}
		if depth == 0 {
			head = strings.TrimSpace(current[:index])
		}
		if inner == "" {
			break
		}
		if json.Valid([]byte(inner)) && strings.Contains(inner, "{") {
			current = inner
			continue
		}
		detail = inner
		break
	}
	if detail == "" && len(codes) > 0 && json.Valid([]byte(current)) {
		detail = compactPreview([]byte(current))
	}
	if head == "" && detail == "" && len(codes) == 0 {
		head = message
	}
	return head, codes, detail
}

func truncateErrorMessage(message string) string {
	runes := []rune(message)
	if len(runes) <= errorMessageLimitRunes {
		return message
	}
	return string(runes[:errorMessageLimitRunes]) + "…（错误信息已截断）"
}

func trimBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	return text
}
