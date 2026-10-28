package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// PreviewRequest 包含一次对话探测请求的全部要素。
type PreviewRequest struct {
	URL     string
	Headers []PreviewHeader
	Body    string
}

// PreviewHeader 是预览输出里的一个请求头。
type PreviewHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// BuildPreviewRequest 生成指定任务的请求预览。stream 决定 Accept 头和 body 里的
// stream 字段，与真实探测保持一致。apiKey 为空时用 <apiKey> 占位，避免把密钥
// 写进可分享的预览文本。
func BuildPreviewRequest(cfg Config, task TaskSpec, stream bool) (PreviewRequest, error) {
	normalized := normalizeTaskSpecs([]TaskSpec{task})
	if len(normalized) == 0 {
		return PreviewRequest{}, fmt.Errorf("invalid task: root/model/protocol must be non-empty")
	}
	modelTask := modelTask{root: normalized[0].Root, protocol: normalized[0].Protocol, model: normalized[0].Model}
	apiKey := cfg.APIKey
	if strings.TrimSpace(apiKey) == "" {
		apiKey = "<apiKey>"
	}
	cfg.APIKey = apiKey
	parts, err := conversationRequestParts(context.Background(), cfg, modelTask, stream)
	if err != nil {
		return PreviewRequest{}, err
	}
	return PreviewRequest{
		URL:     parts.url,
		Headers: parts.headers,
		Body:    parts.body,
	}, nil
}

// requestParts 是 buildConversationRequest 的拆解形态，供真实请求与预览共用。
type requestParts struct {
	url     string
	headers []PreviewHeader
	body    string
}

// conversationRequestParts 组装一次对话探测请求的 URL、请求头和 body。
// buildConversationRequest 与 BuildPreviewRequest 都从这里取数，保证两者
// 生成的请求逐字节一致。
func conversationRequestParts(ctx context.Context, cfg Config, task modelTask, stream bool) (requestParts, error) {
	systemPrompt := ""
	if preset, ok := SystemPromptByID(cfg.SystemPromptPresetID); ok && preset.ID != "none" {
		systemPrompt = preset.SystemPrompt
	}
	body, err := conversationBodyWithSystem(task.protocol, task.model, stream, cfg.Prompt, systemPrompt)
	if err != nil {
		return requestParts{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(task.root, conversationEndpoint(task.protocol)), strings.NewReader(string(body)))
	if err != nil {
		return requestParts{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	authStyle := "openai"
	if task.protocol == ProtocolAnthropicMessages {
		authStyle = "anthropic"
	}
	applyAuthHeaders(req, cfg.APIKey, authStyle)
	applyClientHeaders(req, authStyle, cfg)

	names := make([]string, 0, len(req.Header))
	for name := range req.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	headers := make([]PreviewHeader, 0, len(names))
	for _, name := range names {
		headers = append(headers, PreviewHeader{Name: name, Value: req.Header.Get(name)})
	}
	return requestParts{url: req.URL.String(), headers: headers, body: string(body)}, nil
}

// PreviewKind 是预览输出的格式。
type PreviewKind string

const (
	PreviewCurl PreviewKind = "curl"
	PreviewHTTP PreviewKind = "post"
)

// Preview 生成一段可直接复制执行的请求预览文本。kind 为 curl 时输出
// curl 命令（单引号转义），为 post 时输出 POST + Headers + Body 的可读版。
func Preview(cfg Config, task TaskSpec, stream bool, kind PreviewKind) (string, error) {
	request, err := BuildPreviewRequest(cfg, task, stream)
	if err != nil {
		return "", err
	}
	switch kind {
	case PreviewCurl:
		return previewCurl(request), nil
	case PreviewHTTP:
		return previewPost(request), nil
	default:
		return "", fmt.Errorf("unknown preview kind: %s", kind)
	}
}

// previewCurl 输出一条自包含的 curl 命令；body 以紧凑 JSON 出现在 -d 参数里。
func previewCurl(request PreviewRequest) string {
	var builder strings.Builder
	builder.WriteString("curl -sS -X POST ")
	builder.WriteString(shellQuote(request.URL))
	for _, header := range request.Headers {
		builder.WriteString(" \\\n  -H ")
		builder.WriteString(shellQuote(header.Name + ": " + header.Value))
	}
	builder.WriteString(" \\\n  -d ")
	builder.WriteString(shellQuote(request.Body))
	return builder.String()
}

// previewPost 输出 HTTP 原文形态的请求预览，body 用缩进 JSON 方便阅读。
func previewPost(request PreviewRequest) string {
	var builder strings.Builder
	builder.WriteString("POST ")
	builder.WriteString(request.URL)
	builder.WriteString("\nHeaders:")
	for _, header := range request.Headers {
		builder.WriteString("\n  ")
		builder.WriteString(header.Name)
		builder.WriteString(": ")
		builder.WriteString(header.Value)
	}
	builder.WriteString("\nBody:\n  ")
	builder.WriteString(indentedJSON(request.Body))
	return builder.String()
}

// shellQuote 用单引号包裹并转义内部单引号，保证复制到 POSIX shell 后仍是一个参数。
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// indentedJSON 尽量把紧凑 JSON 展开成两空格缩进；解析失败就原样返回。
func indentedJSON(body string) string {
	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return body
	}
	indented, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return body
	}
	return string(indented)
}

// ModelListPreview 生成模型列表请求的预览文本（curl 或 post 形态），
// 覆盖 NormalizeRoots 展开后的所有 root × 两种鉴权风格。
func ModelListPreview(cfg Config, kind PreviewKind) (string, error) {
	switch kind {
	case PreviewCurl, PreviewHTTP:
	default:
		return "", fmt.Errorf("unknown preview kind: %s", kind)
	}
	roots := NormalizeRoots(cfg.BaseURL)
	apiKey := cfg.APIKey
	if strings.TrimSpace(apiKey) == "" {
		apiKey = "<apiKey>"
	}
	var sections []string
	for _, authStyle := range []string{"openai", "anthropic"} {
		label := "OpenAI 风格（Authorization: Bearer）"
		if authStyle == "anthropic" {
			label = "Anthropic 风格（x-api-key）"
		}
		var commands []string
		for _, root := range roots {
			request, err := modelListPreviewRequest(cfg, root, authStyle, apiKey)
			if err != nil {
				return "", err
			}
			var text string
			if kind == PreviewCurl {
				text = previewCurlGET(request)
			} else {
				text = previewPostGET(request)
			}
			commands = append(commands, text)
		}
		sections = append(sections, "# "+label+"\n"+strings.Join(commands, "\n\n"))
	}
	return strings.Join(sections, "\n\n"), nil
}

// modelListPreviewRequest 组装一个 root 的模型列表请求。
func modelListPreviewRequest(cfg Config, root, authStyle, apiKey string) (PreviewRequest, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, joinURL(root, "/models"), nil)
	if err != nil {
		return PreviewRequest{}, err
	}
	applyAuthHeaders(req, apiKey, authStyle)
	applyClientHeaders(req, authStyle, cfg)
	names := make([]string, 0, len(req.Header))
	for name := range req.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	headers := make([]PreviewHeader, 0, len(names))
	for _, name := range names {
		headers = append(headers, PreviewHeader{Name: name, Value: req.Header.Get(name)})
	}
	return PreviewRequest{URL: req.URL.String(), Headers: headers}, nil
}

// previewCurlGET 输出 GET 请求的 curl 命令（没有 -d）。
func previewCurlGET(request PreviewRequest) string {
	var builder strings.Builder
	builder.WriteString("curl -sS ")
	builder.WriteString(shellQuote(request.URL))
	for _, header := range request.Headers {
		builder.WriteString(" \\\n  -H ")
		builder.WriteString(shellQuote(header.Name + ": " + header.Value))
	}
	return builder.String()
}

// previewPostGET 输出 GET 请求的 HTTP 原文形态预览。
func previewPostGET(request PreviewRequest) string {
	var builder strings.Builder
	builder.WriteString("GET ")
	builder.WriteString(request.URL)
	builder.WriteString("\nHeaders:")
	for _, header := range request.Headers {
		builder.WriteString("\n  ")
		builder.WriteString(header.Name)
		builder.WriteString(": ")
		builder.WriteString(header.Value)
	}
	return builder.String()
}
