package main

import (
	"context"
	"fmt"
	"strings"

	"apicheck/internal/apireq"
	"apicheck/internal/checker"
	"apicheck/internal/presentation"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx context.Context
}

// CheckRequest 是前端提交给桌面绑定的检测请求，字段与 apireq.Request 完全一致。
type CheckRequest = apireq.Request

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) Check(req CheckRequest) (*checker.Report, error) {
	// 作者 long: 桌面端只把表单参数转成检测核心的 Config，真实协议识别仍统一放在 checker.Run，
	// 避免 CLI、Web 预览和桌面 App 之间出现三套判断口径。
	return checker.Run(context.Background(), apireq.ConfigFrom(req, checker.ClientSurfaceDesktop))
}

func (a *App) ListModels(req CheckRequest) (*checker.Report, error) {
	// 作者 long: 获取模型只访问 /models 并生成待检测任务，用户确认模型列表后再触发真实对话探测。
	return checker.ListModels(context.Background(), apireq.ConfigFrom(req, checker.ClientSurfaceDesktop))
}

func (a *App) CheckModels(req CheckRequest) (*checker.Report, error) {
	// 作者 long: 单个检测和批量检测都通过 tasks 明确指定目标，避免重新拉模型列表导致用户筛选结果被刷新。
	cfg := apireq.ConfigFrom(req, checker.ClientSurfaceDesktop)
	if a.ctx != nil && strings.TrimSpace(req.LiveEventName) != "" {
		eventName := strings.TrimSpace(req.LiveEventName)
		cfg.Progress = func(event checker.ProgressEvent) {
			runtime.EventsEmit(a.ctx, eventName, event)
		}
	}
	return checker.CheckModels(context.Background(), cfg, req.Tasks)
}

func (a *App) Preview(payload apireq.PreviewPayload) (string, error) {
	// 作者 long: 预览必须与真实探测逐字节一致，由后端同一套代码生成，前端不再内置协议知识。
	cfg := apireq.ConfigFrom(payload.Request, checker.ClientSurfaceDesktop)
	kind := apireq.ParsePreviewKind(payload.Kind)
	if len(payload.Tasks) > 0 {
		return checker.Preview(cfg, payload.Tasks[0], payload.Stream, kind)
	}
	return checker.ModelListPreview(cfg, kind)
}

// AgentIdentity 返回某个系统提示词预设配套的 agent 请求头（无专属头时为
// 默认身份），供设置面板展示探测实际发送的请求头。
func (a *App) AgentIdentity(presetID string) (checker.AgentIdentity, error) {
	_, identity, ok := checker.AgentIdentityForPreset(presetID)
	if !ok {
		return checker.AgentIdentity{}, fmt.Errorf("unknown preset: %s", presetID)
	}
	return identity, nil
}

// SystemPrompts 返回全部系统提示词预设，供前端渲染切换菜单。
func (a *App) SystemPrompts() []checker.SystemPromptPreset {
	return checker.SystemPromptPresets()
}

func (a *App) RenderMarkdown(text string) (string, error) {
	// 作者 long: 模型返回属于不可信内容，桌面端只接收统一模块清洗后的 HTML，
	// 避免直接把模型生成的脚本或危险链接注入 WebView。
	return presentation.RenderMarkdown(text)
}

func (a *App) GenerateQRCode(text string) (string, error) {
	// 作者 long: 连接分享包含 API Key，二维码必须完全在本地生成，不能调用外部图片或二维码服务。
	return presentation.GenerateQRCodeDataURL(text)
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
