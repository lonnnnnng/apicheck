package checker

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultProbePrompt          = "Reply with exactly: pong"
	defaultRequestTimeout       = 60 * time.Second
	defaultConnectTimeout       = 30 * time.Second
	defaultStreamFirstWait      = 60 * time.Second
	defaultStreamIdleTimeout    = 60 * time.Second
	defaultStreamMaxDuration    = 10 * time.Minute
	defaultProbeMaxTokens       = 64
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
	return &http.Client{Timeout: totalTimeout, Transport: transport}
}

func probeModelSources(ctx context.Context, client *http.Client, cfg Config, root string) []ModelSource {
	var sources []ModelSource
	if shouldProbeOpenAIModels(cfg.Protocol) {
		sources = append(sources, listModels(ctx, client, cfg, root, "openai"))
	}
	if shouldProbeAnthropicModels(cfg.Protocol) {
		sources = append(sources, listModels(ctx, client, cfg, root, "anthropic"))
	}
	return sources
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
		key := task.Root + "\x00" + string(task.Protocol) + "\x00" + task.Model
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, task)
	}
	return out
}

func runTasks(ctx context.Context, cfg Config, tasks []TaskSpec) []ModelResult {
	nonStreamClient := newHTTPClient(cfg, cfg.Timeout, 0)
	streamClient := newHTTPClient(cfg, 0, cfg.StreamFirstTimeout)
	jobs := make(chan modelTask)
	results := make(chan ModelResult)
	workers := cfg.Concurrency
	if workers > len(tasks) {
		workers = len(tasks)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				results <- runTask(ctx, nonStreamClient, streamClient, cfg, task)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, task := range tasks {
			job := modelTask{root: task.Root, protocol: task.Protocol, model: task.Model}
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
	}
	// 按完成序收结果会让同一批次的行序随机波动，排序后保证多次执行可对比。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		return out[i].Root < out[j].Root
	})
	return out
}

func runTask(ctx context.Context, nonStreamClient, streamClient *http.Client, cfg Config, task modelTask) ModelResult {
	result := ModelResult{Root: task.root, Protocol: task.protocol, Model: task.model}
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

func conversationBody(protocol Protocol, model string, stream bool, prompt string) ([]byte, error) {
	return conversationBodyWithSystem(protocol, model, stream, prompt, "")
}

// conversationBodyWithSystem 在探测请求中加入可选 system prompt。
// 三个协议的 system 角色字段名不同（messages[0] / instructions / system），
// 但语义一致：模拟真实 agent 的系统提示词流量。
func conversationBodyWithSystem(protocol Protocol, model string, stream bool, prompt string, systemPrompt string) ([]byte, error) {
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
		body := map[string]any{
			"model":             model,
			"input":             prompt,
			"max_output_tokens": defaultProbeMaxTokens,
			"stream":            stream,
		}
		if systemPrompt != "" {
			body["instructions"] = systemPrompt
		}
		payload = body
	case ProtocolAnthropicMessages:
		body := map[string]any{
			"model":      model,
			"max_tokens": defaultProbeMaxTokens,
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
		result.ErrorMessage = extractErrorMessage(body)
		result.Preview = result.ErrorMessage
		result.RawError = result.RawResponse
		return
	}
	if text := extractConversationText(protocol, body); text != "" {
		result.Preview = text
	} else {
		result.Preview = result.RawResponse
	}
	result.TokenUsage = extractTokenUsage(protocol, body, false)
	result.OK = true
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
		result.ErrorMessage = extractErrorMessage(rawBody)
		result.Preview = result.ErrorMessage
		result.RawError = result.RawResponse
		return
	}
	if text := strings.TrimSpace(capture.textString()); text != "" {
		result.Preview = text
	} else {
		result.Preview = result.RawResponse
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
		capture.writeText(text)
		emitProgress(progress, task, "delta", text, data)
	}
	if result.FirstTextMS == 0 && eventHasText(task.protocol, []byte(data)) {
		result.FirstTextMS = elapsedMS(start)
	}
	// usage 只在最后一个事件里出现（chat 的最终 chunk / responses 的 completed /
	// anthropic 的 message_delta），每次覆盖写入即可拿到最终值。
	if usage := extractTokenUsage(task.protocol, []byte(data), true); usage != (TokenUsage{}) {
		result.TokenUsage = usage
	}
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

func trimBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	return text
}
