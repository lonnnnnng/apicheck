package checker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultProbePrompt = "Reply with exactly: pong"

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
	client := newHTTPClient(cfg)
	report := &Report{Roots: roots, Sources: []ModelSource{}, Tasks: []TaskSpec{}, Results: []ModelResult{}, StartedAt: started}
	for _, root := range roots {
		report.Sources = append(report.Sources, probeModelSources(ctx, client, cfg, root)...)
	}
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
		report.Results = runTasks(ctx, newHTTPClient(cfg), cfg, report.Tasks)
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
		cfg.Timeout = 60 * time.Second
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if strings.TrimSpace(cfg.Prompt) == "" {
		cfg.Prompt = defaultProbePrompt
	}
	cfg.ClientSurface = normalizeClientSurface(cfg.ClientSurface, ClientSurfaceCLI)
	cfg.OpenAIClientSurface = normalizeClientSurface(cfg.OpenAIClientSurface, cfg.ClientSurface)
	cfg.AnthropicClientSurface = normalizeClientSurface(cfg.AnthropicClientSurface, cfg.ClientSurface)
	return cfg
}

func newHTTPClient(cfg Config) *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   cfg.ConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: cfg.ConnectTimeout,
	}
	if cfg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式测试自签名网关时才会打开。
	}
	return &http.Client{Timeout: cfg.Timeout, Transport: transport}
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
	applyClientHeaders(req, authStyle, cfg.clientSurfaceForAuthStyle(authStyle))

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

func runTasks(ctx context.Context, client *http.Client, cfg Config, tasks []TaskSpec) []ModelResult {
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
				results <- runTask(ctx, client, cfg, task)
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
	return out
}

func runTask(ctx context.Context, client *http.Client, cfg Config, task modelTask) ModelResult {
	result := ModelResult{Root: task.root, Protocol: task.protocol, Model: task.model}
	if cfg.Mode == ModeBoth || cfg.Mode == ModeNonStream {
		result.NonStream = probeConversation(ctx, client, cfg, task, false)
	}
	if cfg.Mode == ModeBoth || cfg.Mode == ModeStream {
		result.Stream = probeConversation(ctx, client, cfg, task, true)
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
		result.ErrorClass = "request_failed"
		result.ErrorMessage = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	if stream {
		readStreamResult(resp, task, start, &result, cfg.Progress)
	} else {
		readNonStreamResult(resp, task.protocol, start, &result)
	}
	return result
}

func buildConversationRequest(ctx context.Context, cfg Config, task modelTask, stream bool) (*http.Request, error) {
	endpoint := conversationEndpoint(task.protocol)
	body, err := conversationBody(task.protocol, task.model, stream, cfg.Prompt)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(task.root, endpoint), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if task.protocol == ProtocolAnthropicMessages {
		applyAuthHeaders(req, cfg.APIKey, "anthropic")
		applyClientHeaders(req, "anthropic", cfg.clientSurfaceForAuthStyle("anthropic"))
	} else {
		applyAuthHeaders(req, cfg.APIKey, "openai")
		applyClientHeaders(req, "openai", cfg.clientSurfaceForAuthStyle("openai"))
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
	var payload any
	switch protocol {
	case ProtocolOpenAIChat:
		payload = map[string]any{
			"model":  model,
			"stream": stream,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		}
	case ProtocolOpenAIResponses:
		payload = map[string]any{
			"model":             model,
			"input":             prompt,
			"max_output_tokens": 8,
			"stream":            stream,
		}
	case ProtocolAnthropicMessages:
		payload = map[string]any{
			"model":      model,
			"max_tokens": 8,
			"stream":     stream,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		}
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
	return json.Marshal(payload)
}

func readNonStreamResult(resp *http.Response, protocol Protocol, start time.Time, result *ProbeResult) {
	body, err := io.ReadAll(resp.Body)
	result.TotalMS = elapsedMS(start)
	if err != nil {
		result.ErrorClass = "read_failed"
		result.ErrorMessage = err.Error()
		return
	}
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
	result.OK = true
}

func readStreamResult(resp *http.Response, task modelTask, start time.Time, result *ProbeResult, progress func(ProgressEvent)) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var rawResponse strings.Builder
	var textPreview strings.Builder
	var eventData []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		rawResponse.WriteString(line)
		rawResponse.WriteByte('\n')
		if line == "" {
			handleSSEEvent(task, start, &textPreview, &eventData, result, progress)
			eventData = nil
			continue
		}
		if strings.HasPrefix(line, "data:") {
			eventData = append(eventData, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// 兼容网关在流式探测失败时常直接返回 JSON 错误，而不是 SSE。
			// 保留这些普通行，才能给用户看到真实的鉴权/模型/端点错误。
			appendPreviewLine(&textPreview, line)
		}
	}
	handleSSEEvent(task, start, &textPreview, &eventData, result, progress)
	result.TotalMS = elapsedMS(start)
	if err := scanner.Err(); err != nil {
		result.ErrorClass = "stream_read_failed"
		result.ErrorMessage = err.Error()
		return
	}
	rawBody := []byte(rawResponse.String())
	result.RawResponse = trimBody(rawBody)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.ErrorClass = classifyHTTPError(resp.StatusCode, rawBody)
		result.ErrorMessage = extractErrorMessage(rawBody)
		result.Preview = result.ErrorMessage
		result.RawError = result.RawResponse
		return
	}
	if text := strings.TrimSpace(textPreview.String()); text != "" {
		result.Preview = text
	} else {
		result.Preview = result.RawResponse
	}
	result.OK = true
}

func handleSSEEvent(task modelTask, start time.Time, preview *strings.Builder, eventData *[]string, result *ProbeResult, progress func(ProgressEvent)) {
	if len(*eventData) == 0 {
		return
	}
	data := strings.Join(*eventData, "\n")
	if data == "[DONE]" {
		return
	}
	if text := extractConversationText(task.protocol, []byte(data)); text != "" {
		preview.WriteString(text)
		emitProgress(progress, task, "delta", text, data)
	}
	if result.FirstTextMS == 0 && eventHasText(task.protocol, []byte(data)) {
		result.FirstTextMS = elapsedMS(start)
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

func appendPreviewLine(preview *strings.Builder, line string) {
	preview.WriteString(line)
	preview.WriteByte('\n')
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
