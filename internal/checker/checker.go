package checker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

const (
	defaultProbePrompt       = "Reply with exactly: pong"
	defaultRequestTimeout    = 60 * time.Second
	defaultConnectTimeout    = 30 * time.Second
	defaultStreamFirstWait   = 60 * time.Second
	defaultStreamIdleTimeout = 60 * time.Second
	defaultStreamMaxDuration = 10 * time.Minute
	// 探测请求的 max tokens。设为 1 也能测通，但真实网关配合长系统提示词时
	// 模型往往写到一半就到上限，回一个"截断"事件，容易让人误判成不可用。
	// 256 足够输出一句完整回答，成本仍然极低。
	defaultProbeMaxTokens       = 256
	nonStreamResponseLimitBytes = 2 << 20 // 非流式响应体读取上限，超出部分丢弃并标记截断。
	streamRawLimitBytes         = 2 << 20 // 流式原始响应累积上限。
	streamTextLimitBytes        = 1 << 20 // 流式文本预览累积上限。
	streamTruncatedMarker       = "\n…[truncated]"
)

func Run(ctx context.Context, cfg Config) (*Report, error) {
	report, err := ListModels(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(report.Tasks) == 0 {
		return report, nil
	}
	checkReport, err := CheckModels(ctx, cfg, report.Tasks)
	if err != nil {
		return nil, err
	}
	report.Results = checkReport.Results
	report.FinishedAt = checkReport.FinishedAt
	report.DurationMS = report.FinishedAt.Sub(report.StartedAt).Milliseconds()
	return report, nil
}

func ListModels(ctx context.Context, cfg Config) (*Report, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("base url is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("api key is required")
	}
	cfg = withDefaults(cfg)
	roots := NormalizeRoots(cfg.BaseURL)
	if len(roots) == 0 {
		return nil, fmt.Errorf("invalid base url: %s", cfg.BaseURL)
	}

	started := time.Now()
	client := newHTTPClient(cfg, cfg.Timeout, 0)
	report := &Report{Roots: roots, Sources: []ModelSource{}, Tasks: []TaskSpec{}, Results: []ModelResult{}, StartedAt: started}
	report.Sources = probeAllModelSources(ctx, client, cfg, roots)
	report.Tasks = buildTasks(report.Sources, cfg)
	report.FinishedAt = time.Now()
	report.DurationMS = report.FinishedAt.Sub(started).Milliseconds()
	return report, nil
}

func CheckModels(ctx context.Context, cfg Config, tasks []TaskSpec) (*Report, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("base url is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("api key is required")
	}
	cfg = withDefaults(cfg)
	roots := NormalizeRoots(cfg.BaseURL)
	if len(roots) == 0 {
		return nil, fmt.Errorf("invalid base url: %s", cfg.BaseURL)
	}
	started := time.Now()
	report := &Report{Roots: roots, Sources: []ModelSource{}, Tasks: normalizeTaskSpecs(tasks), Results: []ModelResult{}, StartedAt: started}
	if len(report.Tasks) > 0 {
		report.Results = runTasks(ctx, cfg, report.Tasks)
	}
	report.FinishedAt = time.Now()
	report.DurationMS = report.FinishedAt.Sub(started).Milliseconds()
	return report, nil
}

func withDefaults(cfg Config) Config {
	if cfg.Protocol == "" {
		cfg.Protocol = ProtocolAuto
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeBoth
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultRequestTimeout
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.StreamFirstTimeout <= 0 {
		cfg.StreamFirstTimeout = defaultStreamFirstWait
	}
	if cfg.StreamIdleTimeout <= 0 {
		cfg.StreamIdleTimeout = defaultStreamIdleTimeout
	}
	if cfg.DisableStreamMax {
		cfg.StreamMaxDuration = 0
	} else if cfg.StreamMaxDuration <= 0 {
		cfg.StreamMaxDuration = defaultStreamMaxDuration
	}
	if strings.TrimSpace(cfg.Prompt) == "" {
		cfg.Prompt = defaultProbePrompt
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultProbeMaxTokens
	}
	cfg.ReasoningEffort = normalizeReasoningEffort(cfg.ReasoningEffort)
	cfg.ClientSurface = normalizeClientSurface(cfg.ClientSurface, ClientSurfaceCLI)
	cfg.OpenAIClientSurface = normalizeClientSurface(cfg.OpenAIClientSurface, cfg.ClientSurface)
	cfg.AnthropicClientSurface = normalizeClientSurface(cfg.AnthropicClientSurface, cfg.ClientSurface)
	return cfg
}

func newHTTPClient(cfg Config, totalTimeout, responseHeaderTimeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   cfg.ConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	if cfg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式测试自签名网关时才会打开。
	}
	applyProxy(transport, cfg.Proxy)
	return &http.Client{Timeout: totalTimeout, Transport: transport}
}

// applyProxy 让探测请求走用户指定的出口代理。
// 单独处理 socks5：http.Transport 的 Proxy 只认 http/https 代理。
func applyProxy(transport *http.Transport, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return
	}
	switch strings.ToLower(parsed.Scheme) {
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(parsed, &net.Dialer{Timeout: 30 * time.Second})
		if err != nil {
			return
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		}
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsed)
	}
}

// probeAllModelSources 并行探测所有 root×authStyle 组合（最多 4 个并发），
// 但按 root 顺序、每个 root 内 openai 在 anthropic 之前的固定次序输出，保证多次执行结果稳定可对比。
func probeAllModelSources(ctx context.Context, client *http.Client, cfg Config, roots []string) []ModelSource {
	type probe struct {
		root      string
		authStyle string
	}
	var probes []probe
	for _, root := range roots {
		if shouldProbeOpenAIModels(cfg.Protocol) {
			probes = append(probes, probe{root, "openai"})
		}
		if shouldProbeAnthropicModels(cfg.Protocol) {
			probes = append(probes, probe{root, "anthropic"})
		}
	}
	if len(probes) == 0 {
		return nil
	}
	results := make([]ModelSource, len(probes))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range probes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = listModels(ctx, client, cfg, probes[i].root, probes[i].authStyle)
		}(i)
	}
	wg.Wait()
	return results
}

func shouldProbeOpenAIModels(protocol Protocol) bool {
	switch protocol {
	case ProtocolAuto, ProtocolAll, ProtocolOpenAI, ProtocolOpenAIChat, ProtocolOpenAIResponses:
		return true
	default:
		return false
	}
}

func shouldProbeAnthropicModels(protocol Protocol) bool {
	switch protocol {
	case ProtocolAuto, ProtocolAll, ProtocolAnthropic, ProtocolAnthropicMessages:
		return true
	default:
		return false
	}
}

func listModels(ctx context.Context, client *http.Client, cfg Config, root, authStyle string) ModelSource {
	source := ModelSource{Root: root, AuthStyle: authStyle}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(root, "/models"), nil)
	if err != nil {
		source.Error = err.Error()
		return source
	}
	req.Header.Set("Accept", "application/json")
	applyAuthHeaders(req, cfg.APIKey, authStyle)
	applyClientHeaders(req, authStyle, cfg)
	applyCustomHeaders(req, cfg)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		source.Error = err.Error()
		return source
	}
	defer resp.Body.Close()
	source.HTTPStatus = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		source.Error = err.Error()
		return source
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		source.Error = fmt.Sprintf("%s after %dms", classifyHTTPError(resp.StatusCode, body), elapsedMS(start))
		return source
	}
	models, err := parseModelIDs(body)
	if err != nil {
		source.Error = err.Error()
		return source
	}
	source.Models = filterModels(models, cfg.Filter, cfg.MaxModels)
	return source
}

func applyAuthHeaders(req *http.Request, apiKey, authStyle string) {
	if authStyle == "anthropic" {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
}

func buildTasks(sources []ModelSource, cfg Config) []TaskSpec {
	seen := make(map[string]struct{})
	var tasks []TaskSpec
	add := func(root string, protocol Protocol, models []string) {
		for _, model := range models {
			key := root + "\x00" + string(protocol) + "\x00" + model
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			tasks = append(tasks, TaskSpec{Root: root, Protocol: protocol, Model: model})
		}
	}
	for _, source := range sources {
		if len(source.Models) == 0 {
			continue
		}
		// 同一个模型列表可能来自聚合网关，不能只按鉴权头认定协议；这里保留协议维度，
		// 让后续真实对话请求来确认每个模型到底支持哪条标准端点。
		switch cfg.Protocol {
		case ProtocolOpenAIChat:
			add(source.Root, ProtocolOpenAIChat, source.Models)
		case ProtocolOpenAIResponses:
			add(source.Root, ProtocolOpenAIResponses, source.Models)
		case ProtocolAnthropicMessages:
			add(source.Root, ProtocolAnthropicMessages, source.Models)
		case ProtocolOpenAI:
			add(source.Root, ProtocolOpenAIChat, source.Models)
			add(source.Root, ProtocolOpenAIResponses, source.Models)
		case ProtocolAnthropic:
			add(source.Root, ProtocolAnthropicMessages, source.Models)
		default:
			if source.AuthStyle == "anthropic" {
				add(source.Root, ProtocolAnthropicMessages, source.Models)
			} else {
				add(source.Root, ProtocolOpenAIChat, source.Models)
				add(source.Root, ProtocolOpenAIResponses, source.Models)
			}
		}
	}
	return tasks
}

func normalizeTaskSpecs(tasks []TaskSpec) []TaskSpec {
	seen := make(map[string]struct{})
	out := make([]TaskSpec, 0, len(tasks))
	for _, task := range tasks {
		task.Root = strings.TrimSpace(task.Root)
		task.Model = strings.TrimSpace(task.Model)
		if task.Root == "" || task.Model == "" || task.Protocol == "" {
			continue
		}
		key := task.AccountID + "\x00" + task.Root + "\x00" + string(task.Protocol) + "\x00" + task.Model
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, task)
	}
	return out
}

func runTasks(ctx context.Context, cfg Config, tasks []TaskSpec) []ModelResult {
	onResult := cfg.OnResult
	nonStreamClient := newHTTPClient(cfg, cfg.Timeout, 0)
	streamClient := newHTTPClient(cfg, 0, cfg.StreamFirstTimeout)
	jobs := make(chan modelTask)
	results := make(chan ModelResult)
	workers := cfg.Concurrency
	if workers > len(tasks) {
		workers = len(tasks)
	}
	if workers < 1 {
		workers = 1
	}
	// 作者 long: 自适应并发。被网关限流时把"同时进行"的请求数上限压下来，
	// 已启动的 worker 不退出，只是排队等待许可，避免越试越限。
	var permits atomic.Int64
	permits.Store(int64(workers))
	if cfg.AdaptiveConcurrency {
		permits.Store(1)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				if cfg.AdaptiveConcurrency {
					acquirePermit(&permits)
				}
				result, throttled := runTaskWithRetry(ctx, nonStreamClient, streamClient, cfg, task)
				if cfg.AdaptiveConcurrency {
					if throttled {
						halvePermits(&permits, workers)
					} else {
						permits.Add(1)
						if permits.Load() > int64(workers) {
							permits.Store(int64(workers))
						}
					}
				}
				results <- result
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, task := range tasks {
			job := modelTask{accountID: task.AccountID, root: task.Root, protocol: task.Protocol, model: task.Model}
			select {
			case <-ctx.Done():
				return
			case jobs <- job:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	out := make([]ModelResult, 0, len(tasks))
	for result := range results {
		out = append(out, result)
		if onResult != nil {
			onResult(result)
		}
	}
	// 按完成序收结果会让同一批次的行序随机波动，排序后保证多次执行可对比。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		if out[i].Root != out[j].Root {
			return out[i].Root < out[j].Root
		}
		return out[i].AccountID < out[j].AccountID
	})
	return out
}

func runTask(ctx context.Context, nonStreamClient, streamClient *http.Client, cfg Config, task modelTask) ModelResult {
	result := ModelResult{AccountID: task.accountID, Root: task.root, Protocol: task.protocol, Model: task.model}
	if cfg.Mode == ModeBoth || cfg.Mode == ModeNonStream {
		result.NonStream = probeConversation(ctx, nonStreamClient, cfg, task, false)
	}
	if cfg.Mode == ModeBoth || cfg.Mode == ModeStream {
		result.Stream = probeConversation(ctx, streamClient, cfg, task, true)
	}
	return result
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
