package main

import (
	"context"
	"strings"
	"time"

	"apicheck/internal/checker"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx context.Context
}

type CheckRequest struct {
	BaseURL            string             `json:"base_url"`
	APIKey             string             `json:"api_key"`
	Prompt             string             `json:"prompt"`
	Protocol           string             `json:"protocol"`
	Mode               string             `json:"mode"`
	Concurrency        int                `json:"concurrency"`
	Timeout            string             `json:"timeout"`
	StreamFirstTimeout string             `json:"stream_first_timeout"`
	StreamIdleTimeout  string             `json:"stream_idle_timeout"`
	StreamMaxDuration  string             `json:"stream_max_duration"`
	Filter             string             `json:"filter"`
	MaxModels          int                `json:"max_models"`
	SkipTLSVerify      bool               `json:"skip_tls_verify"`
	ConnectTimeout     string             `json:"connect_timeout"`
	OpenAIUASurface    string             `json:"openai_ua_surface"`
	AnthropicUASurface string             `json:"anthropic_ua_surface"`
	LiveEventName      string             `json:"live_event_name"`
	Tasks              []checker.TaskSpec `json:"tasks"`
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) Check(req CheckRequest) (*checker.Report, error) {
	// 作者 long: 桌面端只把表单参数转成检测核心的 Config，真实协议识别仍统一放在 checker.Run，
	// 避免 CLI、Web 预览和桌面 App 之间出现三套判断口径。
	return checker.Run(context.Background(), configFromRequest(req))
}

func (a *App) ListModels(req CheckRequest) (*checker.Report, error) {
	// 作者 long: 获取模型只访问 /models 并生成待检测任务，用户确认模型列表后再触发真实对话探测。
	return checker.ListModels(context.Background(), configFromRequest(req))
}

func (a *App) CheckModels(req CheckRequest) (*checker.Report, error) {
	// 作者 long: 单个检测和批量检测都通过 tasks 明确指定目标，避免重新拉模型列表导致用户筛选结果被刷新。
	cfg := configFromRequest(req)
	if a.ctx != nil && strings.TrimSpace(req.LiveEventName) != "" {
		eventName := strings.TrimSpace(req.LiveEventName)
		cfg.Progress = func(event checker.ProgressEvent) {
			runtime.EventsEmit(a.ctx, eventName, event)
		}
	}
	return checker.CheckModels(context.Background(), cfg, req.Tasks)
}

func configFromRequest(req CheckRequest) checker.Config {
	timeout := parseDurationOrDefault(req.Timeout, 60*time.Second)
	connectTimeout := parseDurationOrDefault(req.ConnectTimeout, 30*time.Second)
	streamMaxDuration, disableStreamMax := parseStreamMaxDuration(req.StreamMaxDuration, 10*time.Minute)
	return checker.Config{
		BaseURL:                req.BaseURL,
		APIKey:                 req.APIKey,
		Prompt:                 req.Prompt,
		Protocol:               checker.Protocol(strings.TrimSpace(req.Protocol)),
		Mode:                   checker.Mode(strings.TrimSpace(req.Mode)),
		Concurrency:            req.Concurrency,
		Timeout:                timeout,
		ConnectTimeout:         connectTimeout,
		StreamFirstTimeout:     parseDurationOrDefault(req.StreamFirstTimeout, 60*time.Second),
		StreamIdleTimeout:      parseDurationOrDefault(req.StreamIdleTimeout, 60*time.Second),
		StreamMaxDuration:      streamMaxDuration,
		DisableStreamMax:       disableStreamMax,
		Filter:                 req.Filter,
		MaxModels:              req.MaxModels,
		InsecureSkipVerify:     req.SkipTLSVerify,
		ClientSurface:          checker.ClientSurfaceDesktop,
		OpenAIClientSurface:    parseClientSurfaceOrDefault(req.OpenAIUASurface, checker.ClientSurfaceDesktop),
		AnthropicClientSurface: parseClientSurfaceOrDefault(req.AnthropicUASurface, checker.ClientSurfaceDesktop),
	}
}

func (a *App) Quit() {
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
}

func (a *App) Minimise() {
	if a.ctx != nil {
		runtime.WindowMinimise(a.ctx)
	}
}

func (a *App) ToggleMaximise() {
	if a.ctx != nil {
		runtime.WindowToggleMaximise(a.ctx)
	}
}

func parseDurationOrDefault(value string, fallback time.Duration) time.Duration {
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

func parseStreamMaxDuration(value string, fallback time.Duration) (time.Duration, bool) {
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

func parseClientSurfaceOrDefault(value string, fallback checker.ClientSurface) checker.ClientSurface {
	switch checker.ClientSurface(strings.ToLower(strings.TrimSpace(value))) {
	case checker.ClientSurfaceCLI:
		return checker.ClientSurfaceCLI
	case checker.ClientSurfaceDesktop:
		return checker.ClientSurfaceDesktop
	default:
		return fallback
	}
}
