package checker

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

func execCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// 作者 long: 每个 Agent 预设配套一组真实客户端请求头。很多中转站会按
// User-Agent / originator 等特征识别 agent 流量并拦截“测活”请求，因此
// 选了某个系统提示词预设时也应发出该 agent 的真实请求头，两者保持一致。
//
// 数据来源（源码/二进制核对）：
//   - pi:   @earendil-works/pi-ai dist/utils/identity.js 的 formatUserAgent
//   - codex: openai/codex rust-v0.165.0 codex-rs（originator + UA 公式）
//   - opencode: sst/opencode 1.19.5（UA opencode/<version>）
//   - claude-code（pi 的 Anthropic OAuth 通道使用 claude-cli/<version>）
const (
	piClientVersion         = "1.4.0"
	opencodeClientVersion   = "1.19.5"
	anthropicSDKVersion     = "0.129.0"
	claudeCodeOAuthVersion  = "2.1.280"
	codexClientVersion      = "0.165.0"
	claudeCodeClientVersion = "2.1.312"
)

// AgentIdentity 是一个 agent 预设的客户端身份：按鉴权风格返回要附加的请求头。
type AgentIdentity struct {
	// OpenAI 是 OpenAI 风格端点（/chat/completions、/responses）使用的头。
	OpenAI []PreviewHeader `json:"openai"`
	// Anthropic 是 Anthropic Messages 端点使用的头。
	Anthropic []PreviewHeader `json:"anthropic"`
}

var osVersionCache struct {
	once  sync.Once
	value string
}

func osVersion() string {
	osVersionCache.once.Do(func() {
		osVersionCache.value = "unknown"
		if runtime.GOOS == "darwin" {
			out, err := execCommand("sw_vers", "-productVersion")
			if err == nil {
				if value := strings.TrimSpace(out); value != "" {
					osVersionCache.value = value
				}
			}
		}
	})
	return osVersionCache.value
}

func osName() string {
	switch runtime.GOOS {
	case "darwin":
		return "Mac OS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

func osArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func sanitizeHeaderValue(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r >= 32 && r <= 126 {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

// agentIdentityByID 返回预设对应的客户端请求头；none/未知预设返回 nil。
func agentIdentityByID(id string) map[string][]PreviewHeader {
	switch id {
	case "pi":
		return map[string][]PreviewHeader{
			"openai": {{"User-Agent", piUserAgent()}},
			// pi 的 Anthropic 通道走 @anthropic-ai/sdk，除 UA 外还带 SDK 的
			// x-stainless-* 指纹头；anthropic-version 由鉴权层统一设置。
			"anthropic": append([]PreviewHeader{{"User-Agent", piUserAgent()}}, stainlessHeaders()...),
		}
	case "codex":
		originator := "codex_cli_rs"
		return map[string][]PreviewHeader{
			"openai": {
				{"User-Agent", codexUserAgent(originator)},
				{"originator", originator},
			},
			"anthropic": {
				{"User-Agent", codexUserAgent(originator)},
				{"originator", originator},
			},
		}
	case "opencode":
		return map[string][]PreviewHeader{
			"openai":    {{"User-Agent", "opencode/" + opencodeClientVersion}},
			"anthropic": {{"User-Agent", "opencode/" + opencodeClientVersion}},
		}
	default:
		// deepseek 等未公开客户端请求头的预设：不附加 agent 头。
		return nil
	}
}

// AgentIdentityForPreset 返回某个系统提示词预设配套的请求头（含展示名）。
// 预设没有专属请求头时返回本工具的默认身份，保证前端总能展示探测实际
// 发送的头，而不是留空。
func AgentIdentityForPreset(id string) (SystemPromptPreset, AgentIdentity, bool) {
	preset, ok := SystemPromptByID(id)
	if !ok {
		return SystemPromptPreset{}, AgentIdentity{}, false
	}
	headers := agentIdentityByID(preset.ID)
	if headers == nil {
		headers = defaultIdentityHeaders()
	}
	return preset, AgentIdentity{OpenAI: headers["openai"], Anthropic: headers["anthropic"]}, true
}

// defaultIdentityHeaders 是未选 agent 预设时探测请求实际发送的头，
// 与 clientHeaders 的默认分支保持一致。
func defaultIdentityHeaders() map[string][]PreviewHeader {
	originator := codexOriginator(ClientSurfaceCLI)
	return map[string][]PreviewHeader{
		"openai": {
			{"User-Agent", codexUserAgent(originator)},
			{"originator", originator},
		},
		"anthropic": {
			{"User-Agent", claudeCodeUserAgent()},
			{"anthropic-client-platform", claudeCodeClientPlatform(ClientSurfaceCLI)},
		},
	}
}

// AgentIdentityPreviews 返回全部带配套请求头的预设，供前端展示与文档。
func AgentIdentityPreviews() []struct {
	Preset   SystemPromptPreset `json:"preset"`
	Identity AgentIdentity      `json:"identity"`
} {
	var result []struct {
		Preset   SystemPromptPreset `json:"preset"`
		Identity AgentIdentity      `json:"identity"`
	}
	for _, id := range []string{"pi", "codex", "codex-gpt-5", "opencode", "deepseek", "deepseek-web"} {
		preset, identity, ok := AgentIdentityForPreset(id)
		if !ok {
			continue
		}
		if len(identity.OpenAI) == 0 && len(identity.Anthropic) == 0 {
			continue
		}
		result = append(result, struct {
			Preset   SystemPromptPreset `json:"preset"`
			Identity AgentIdentity      `json:"identity"`
		}{preset, identity})
	}
	return result
}

func piUserAgent() string {
	// pi 的 detailed UA：pi/<version> (<platform>; node/<runtime>; <arch>)
	return sanitizeHeaderValue(fmt.Sprintf("pi/%s (%s; node/v22.11.0; %s)", piClientVersion, runtime.GOOS, runtime.GOARCH))
}

func codexUserAgent(originator string) string {
	// codex UA 公式：<originator>/<version> (<os> <version>; <arch>) <terminal>
	return sanitizeHeaderValue(fmt.Sprintf("%s/%s (%s %s; %s) unknown", originator, codexClientVersion, osName(), osVersion(), osArch()))
}

// stainlessHeaders 复刻 @anthropic-ai/sdk 在 Node 环境发送的 x-stainless-* 头。
func stainlessHeaders() []PreviewHeader {
	return []PreviewHeader{
		{"X-Stainless-Lang", "js"},
		{"X-Stainless-Package-Version", anthropicSDKVersion},
		{"X-Stainless-OS", stainlessOS()},
		{"X-Stainless-Arch", runtime.GOARCH},
		{"X-Stainless-Runtime", "node"},
		{"X-Stainless-Runtime-Version", "v22.11.0"},
	}
}

func stainlessOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return "Unknown"
	}
}

// claudeCodeUserAgent 是 pi 在 Anthropic OAuth 通道使用的 Claude Code 身份；
// API-key 通道不使用，保留给未来 OAuth 探测模式。
func claudeCodeUserAgent() string {
	return "claude-cli/" + claudeCodeOAuthVersion
}
