package checker

import (
	"embed"
	"sort"
	"strings"
	"sync"
)

// 作者 long: 这些提示词是四个开源 coding agent 的真实系统提示词快照，
// 用于模拟“真实 agent 流量”做网关兼容性探测——很多网关会按 system
// prompt 的特征（长度、语言、结构）路由或限流，只用 "pong" 探不出这些差异。
//
//go:embed systemprompt/*.md
var systemPromptFS embed.FS

// SystemPromptPreset 是一份可切换的系统提示词预设。
type SystemPromptPreset struct {
	// ID 是稳定标识（也是文件名去掉扩展名），前端切换时引用它。
	ID string `json:"id"`
	// Name 是展示名。
	Name string `json:"name"`
	// Description 一句话说明来源与用途。
	Description string `json:"description"`
	// SystemPrompt 是完整提示词正文。
	SystemPrompt string `json:"system_prompt"`
}

// presetMeta 描述每个预设的来源；正文以文件形式维护，避免超长字符串塞进 Go 源码。
var presetMeta = []struct {
	id          string
	name        string
	description string
}{
	{"none", "无（默认）", "不发送 system prompt，仅发送探测用户消息。"},
	{"pi", "pi coding agent", "pi（@earendil-works/pi-coding-agent）的结构化系统提示词：preamble/tools/rules/cwd 分段。"},
	{"codex", "Codex CLI 基础指令", "openai/codex codex-rs 的 base instructions，通用沙箱与协作规范。"},
	{"codex-gpt-5", "Codex CLI (GPT-5)", "openai/codex 针对 GPT-5 系模型的系统提示词，含编辑约束与 review 心态。"},
	{"opencode", "OpenCode", "sst/opencode 的 GPT 系系统提示词，含注释密度与破坏性操作约束。"},
	{"deepseek", "DeepSeek Harness (ACP)", "deepseek-harness 渲染快照：读文件/后台任务/目标工具等运行时规则。"},
	{"deepseek-web", "DeepSeek Harness (Web)", "deepseek-harness Web GUI 模式的渲染快照，含 {{webUrl}} 等模板变量。"},
}

var (
	presetOnce    sync.Once
	presetCache   []SystemPromptPreset
	presetByIDMap map[string]SystemPromptPreset
)

func loadPresets() {
	presetOnce.Do(func() {
		presetByIDMap = map[string]SystemPromptPreset{}
		for _, meta := range presetMeta {
			preset := SystemPromptPreset{ID: meta.id, Name: meta.name, Description: meta.description}
			if meta.id != "none" {
				data, err := systemPromptFS.ReadFile("systemprompt/" + meta.id + ".md")
				if err != nil {
					continue
				}
				preset.SystemPrompt = strings.TrimRight(string(data), "\n")
			}
			presetCache = append(presetCache, preset)
			presetByIDMap[preset.ID] = preset
		}
	})
}

// SystemPromptPresets 返回全部预设，顺序与 presetMeta 一致（none 恒在最前）。
func SystemPromptPresets() []SystemPromptPreset {
	loadPresets()
	return append([]SystemPromptPreset(nil), presetCache...)
}

// SystemPromptByID 按 ID 查找预设；"none" 与空串返回无 system prompt 的占位预设。
// 找不到时第二个返回值为 false。
func SystemPromptByID(id string) (SystemPromptPreset, bool) {
	loadPresets()
	if id == "" {
		id = "none"
	}
	preset, ok := presetByIDMap[id]
	return preset, ok
}

// SystemPromptIDs 返回全部预设 ID（排序后），用于校验与文档。
func SystemPromptIDs() []string {
	presets := SystemPromptPresets()
	ids := make([]string, 0, len(presets))
	for _, preset := range presets {
		ids = append(ids, preset.ID)
	}
	sort.Strings(ids)
	return ids
}
