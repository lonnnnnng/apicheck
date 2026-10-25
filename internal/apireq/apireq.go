// Package apireq 汇总 Web /api 端点与桌面 Wails 绑定共用的请求结构与解析助手，
// 避免两个入口各维护一份字段映射后出现行为漂移。
package apireq

import (
	"strings"
	"time"

	"apicheck/internal/checker"
)

// Request 是前端提交给检测入口的统一请求体，JSON 字段与页面 checkRequest 一一对应。
type Request struct {
	BaseURL                 string             `json:"base_url"`
	APIKey                  string             `json:"api_key"`
	Prompt                  string             `json:"prompt"`
	SystemPromptPresetID    string             `json:"system_prompt_preset"`
	Protocol                string             `json:"protocol"`
	Mode                    string             `json:"mode"`
	Concurrency             int                `json:"concurrency"`
	Timeout                 string             `json:"timeout"`
	StreamFirstTimeout      string             `json:"stream_first_timeout"`
	StreamIdleTimeout       string             `json:"stream_idle_timeout"`
	StreamMaxDuration       string             `json:"stream_max_duration"`
	Filter                  string             `json:"filter"`
	MaxModels               int                `json:"max_models"`
	SkipTLSVerify           bool               `json:"skip_tls_verify"`
	ConnectTimeout          string             `json:"connect_timeout"`
	OpenAIUASurface         string             `json:"openai_ua_surface"`
	AnthropicUASurface      string             `json:"anthropic_ua_surface"`
	OpenAIUserAgent         string             `json:"openai_user_agent"`
	OpenAIOriginator        string             `json:"openai_originator"`
	AnthropicUserAgent      string             `json:"anthropic_user_agent"`
	AnthropicClientPlatform string             `json:"anthropic_client_platform"`
	LiveEventName           string             `json:"live_event_name"`
	Tasks                   []checker.TaskSpec `json:"tasks"`
}

// PreviewPayload 在 Request 基础上附加预览专用字段，供 /api/preview 与桌面 Preview 绑定使用。
// Kind 为 "curl" 或 "post"；Tasks 为空时预览模型列表请求，否则预览第一个任务的对话请求。
type PreviewPayload struct {
	Request
	Stream bool   `json:"stream"`
	Kind   string `json:"kind"`
}

// ParsePreviewKind 归一化预览格式，非法值回退 curl。
func ParsePreviewKind(kind string) checker.PreviewKind {
	if checker.PreviewKind(kind) == checker.PreviewHTTP {
		return checker.PreviewHTTP
	}
	return checker.PreviewCurl
}

// ConfigFrom 把前端请求转换成检测核心 Config；defaultSurface 是该入口的默认客户端标识形态
// （Web 为 cli，桌面为 desktop）。
func ConfigFrom(req Request, defaultSurface checker.ClientSurface) checker.Config {
	streamMaxDuration, disableStreamMax := ParseStreamMaxDuration(req.StreamMaxDuration, 10*time.Minute)
	return checker.Config{
		BaseURL:                 req.BaseURL,
		APIKey:                  req.APIKey,
		Prompt:                  req.Prompt,
		SystemPromptPresetID:    strings.TrimSpace(req.SystemPromptPresetID),
		Protocol:                checker.Protocol(strings.TrimSpace(req.Protocol)),
		Mode:                    checker.Mode(strings.TrimSpace(req.Mode)),
		Concurrency:             req.Concurrency,
		Timeout:                 ParseDurationOrDefault(req.Timeout, 60*time.Second),
		ConnectTimeout:          ParseDurationOrDefault(req.ConnectTimeout, 30*time.Second),
		StreamFirstTimeout:      ParseDurationOrDefault(req.StreamFirstTimeout, 60*time.Second),
		StreamIdleTimeout:       ParseDurationOrDefault(req.StreamIdleTimeout, 60*time.Second),
		StreamMaxDuration:       streamMaxDuration,
		DisableStreamMax:        disableStreamMax,
		Filter:                  req.Filter,
		MaxModels:               req.MaxModels,
		InsecureSkipVerify:      req.SkipTLSVerify,
		ClientSurface:           defaultSurface,
		OpenAIClientSurface:     ParseClientSurfaceOrDefault(req.OpenAIUASurface, defaultSurface),
		AnthropicClientSurface:  ParseClientSurfaceOrDefault(req.AnthropicUASurface, defaultSurface),
		OpenAIUserAgent:         req.OpenAIUserAgent,
		OpenAIOriginator:        req.OpenAIOriginator,
		AnthropicUserAgent:      req.AnthropicUserAgent,
		AnthropicClientPlatform: req.AnthropicClientPlatform,
	}
}

// ParseClientSurfaceOrDefault 解析 cli/desktop 客户端标识形态，非法值回退 fallback。
func ParseClientSurfaceOrDefault(value string, fallback checker.ClientSurface) checker.ClientSurface {
	switch checker.ClientSurface(strings.ToLower(strings.TrimSpace(value))) {
	case checker.ClientSurfaceCLI:
		return checker.ClientSurfaceCLI
	case checker.ClientSurfaceDesktop:
		return checker.ClientSurfaceDesktop
	default:
		return fallback
	}
}

// ParseDurationOrDefault 解析 "60s"/"10m" 形式的时长，空值或非法值回退 fallback。
func ParseDurationOrDefault(value string, fallback time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// ParseStreamMaxDuration 解析流式最长时长；显式 0 表示不限制，返回 disable=true。
func ParseStreamMaxDuration(value string, fallback time.Duration) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, false
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback, false
	}
	if parsed == 0 {
		return 0, true
	}
	return parsed, false
}
