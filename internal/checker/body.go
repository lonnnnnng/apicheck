package checker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func buildConversationRequest(ctx context.Context, cfg Config, task modelTask, stream bool) (*http.Request, error) {
	parts, err := conversationRequestParts(ctx, cfg, task, stream)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parts.url, strings.NewReader(parts.body))
	if err != nil {
		return nil, err
	}
	for _, header := range parts.headers {
		req.Header.Set(header.Name, header.Value)
	}
	return req, nil
}

func conversationEndpoint(protocol Protocol) string {
	switch protocol {
	case ProtocolOpenAIChat:
		return "/chat/completions"
	case ProtocolOpenAIResponses:
		return "/responses"
	case ProtocolAnthropicMessages:
		return "/messages"
	default:
		return "/chat/completions"
	}
}

// 作者 long: 一些中转网关（new-api 系，例如 anyrouter）在 /responses 上强制要求
// prompt_cache_key，缺了直接回 400 invalid_responses_request（"prompt_cache_key is required"）。
// 官方接口里它是可选的缓存路由键，真实 codex 客户端每轮都会带一个会话级 UUID。
// 这里按模型名派生一个 UUID 形状的稳定值：既能过校验，重复探测也落在同一缓存分区。
func probePromptCacheKey(model string) string {
	sum := sha256.Sum256([]byte("apicheck|" + model))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// normalizeReasoningEffort 把用户输入归一化成合法等级；无法识别时返回空（不发送字段）。
func normalizeReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ReasoningEffortMinimal:
		return ReasoningEffortMinimal
	case ReasoningEffortLow:
		return ReasoningEffortLow
	case ReasoningEffortMedium:
		return ReasoningEffortMedium
	case ReasoningEffortHigh:
		return ReasoningEffortHigh
	default:
		return ""
	}
}

// applyReasoningEffort 按协议把"模型等级"写进请求体。
// 各协议字段名不同，且 anthropic 不认 minimal，这里统一降级成 low，避免网关 400。
func applyReasoningEffort(body map[string]any, protocol Protocol, effort string) {
	effort = normalizeReasoningEffort(effort)
	if effort == "" {
		return
	}
	switch protocol {
	case ProtocolOpenAIChat:
		body["reasoning_effort"] = effort
	case ProtocolOpenAIResponses:
		body["reasoning"] = map[string]any{"effort": effort}
	case ProtocolAnthropicMessages:
		if effort == ReasoningEffortMinimal {
			effort = ReasoningEffortLow
		}
		body["output_config"] = map[string]any{"effort": effort}
	}
}

func conversationBody(protocol Protocol, model string, stream bool, prompt string) ([]byte, error) {
	return conversationBodyWithOptions(protocol, model, stream, prompt, "", 0)
}

// conversationBodyWithSystem 在探测请求中加入可选 system prompt。
func conversationBodyWithSystem(protocol Protocol, model string, stream bool, prompt string, systemPrompt string) ([]byte, error) {
	return conversationBodyWithOptions(protocol, model, stream, prompt, systemPrompt, 0)
}

// conversationBodyWithEffort 在探测请求体中加入"模型等级"。
func conversationBodyWithEffort(protocol Protocol, model string, stream bool, prompt string, systemPrompt string, maxTokens int, effort string) ([]byte, error) {
	return buildConversationBody(protocol, model, stream, prompt, systemPrompt, maxTokens, effort)
}

// conversationBodyWithOptions 是探测请求体的唯一构造入口。
// 三个协议的 system 角色字段名不同（messages[0] / instructions / system），
// 但语义一致：模拟真实 agent 的系统提示词流量。
// maxTokens <=0 时回退 defaultProbeMaxTokens；maxTokensLimit 由调用方（cfg 已归一化）保证合法。
func conversationBodyWithOptions(protocol Protocol, model string, stream bool, prompt string, systemPrompt string, maxTokens int) ([]byte, error) {
	return buildConversationBody(protocol, model, stream, prompt, systemPrompt, maxTokens, "")
}

func buildConversationBody(protocol Protocol, model string, stream bool, prompt string, systemPrompt string, maxTokens int, effort string) ([]byte, error) {
	if maxTokens <= 0 {
		maxTokens = defaultProbeMaxTokens
	}
	var payload any
	switch protocol {
	case ProtocolOpenAIChat:
		messages := []map[string]string{}
		if systemPrompt != "" {
			messages = append(messages, map[string]string{"role": "system", "content": systemPrompt})
		}
		messages = append(messages, map[string]string{"role": "user", "content": prompt})
		payload = map[string]any{
			"model":    model,
			"stream":   stream,
			"messages": messages,
		}
	case ProtocolOpenAIResponses:
		// 作者 long: input 用数组形态发，和真实 codex 一致。
		// 某些中转网关（new-api 系）只认数组，传字符串会回 400 "input is required"。
		body := map[string]any{
			"model":             model,
			"input":             []map[string]any{{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": prompt}}}},
			"max_output_tokens": maxTokens,
			"stream":            stream,
			"store":             false,
			"prompt_cache_key":  probePromptCacheKey(model),
		}
		if systemPrompt != "" {
			body["instructions"] = systemPrompt
		}
		payload = body
	case ProtocolAnthropicMessages:
		body := map[string]any{
			"model":      model,
			"max_tokens": maxTokens,
			"stream":     stream,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		}
		if systemPrompt != "" {
			body["system"] = systemPrompt
		}
		payload = body
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
	if body, ok := payload.(map[string]any); ok {
		applyReasoningEffort(body, protocol, effort)
	}
	return json.Marshal(payload)
}

func probeConversation(ctx context.Context, client *http.Client, cfg Config, task modelTask, stream bool) ProbeResult {
	result := ProbeResult{Tested: true}
	req, err := buildConversationRequest(ctx, cfg, task, stream)
	if err != nil {
		result.ErrorClass = "request_build_failed"
		result.ErrorMessage = err.Error()
		return result
	}
	start := time.Now()
	resp, err := client.Do(req)
	firstByte := time.Since(start)
	result.FirstByteMS = durationMS(firstByte)
	if err != nil {
		if stream && isTimeoutError(err) {
			result.ErrorClass = "stream_first_timeout"
		} else {
			result.ErrorClass = "request_failed"
		}
		result.ErrorMessage = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	if stream {
		readStreamResult(resp, task, start, cfg, &result)
	} else {
		readNonStreamResult(resp, task.protocol, start, &result)
	}
	return result
}
