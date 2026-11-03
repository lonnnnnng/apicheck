package checker

import (
	"net/http"
	"strings"
)

// clientHeader 是要附加到请求上的单个客户端头。
type clientHeader struct {
	name  string
	value string
}

func normalizeClientSurface(surface, fallback ClientSurface) ClientSurface {
	switch surface {
	case ClientSurfaceCLI, ClientSurfaceDesktop:
		return surface
	default:
		return fallback
	}
}

func (cfg Config) clientSurfaceForAuthStyle(authStyle string) ClientSurface {
	if authStyle == "anthropic" {
		return cfg.AnthropicClientSurface
	}
	return cfg.OpenAIClientSurface
}

func applyClientHeaders(req *http.Request, authStyle string, cfg Config) {
	for _, header := range clientHeaders(authStyle, cfg) {
		req.Header.Set(header.name, header.value)
	}
}

// clientHeaders 返回一次请求应附加的客户端请求头。选择带配套身份的
// agent 预设（pi/codex/opencode）时返回该 agent 的真实请求头；否则返回
// 本工具自身的探测身份（历史默认形态：codex 结构 UA + claude-code UA）。
// 自定义 UA 字段已移除：模拟真实 agent 流量时自定义值反而会破坏一致性。
func clientHeaders(authStyle string, cfg Config) []clientHeader {
	if identity := agentIdentityByID(cfg.SystemPromptPresetID); identity != nil {
		if headers, ok := identity[authStyle]; ok && len(headers) > 0 {
			result := make([]clientHeader, 0, len(headers))
			for _, header := range headers {
				result = append(result, clientHeader{name: header.Name, value: header.Value})
			}
			return result
		}
	}
	surface := cfg.clientSurfaceForAuthStyle(authStyle)
	if authStyle == "anthropic" {
		return []clientHeader{
			{name: "User-Agent", value: claudeCodeUserAgent()},
			{name: "anthropic-client-platform", value: claudeCodeClientPlatform(surface)},
		}
	}
	originator := codexOriginator(surface)
	return []clientHeader{
		{name: "User-Agent", value: codexUserAgent(originator)},
		{name: "originator", value: originator},
	}
}

func codexOriginator(surface ClientSurface) string {
	if surface == ClientSurfaceDesktop {
		return "codex_chatgpt_desktop"
	}
	return "codex_cli_rs"
}

func claudeCodeClientPlatform(surface ClientSurface) string {
	if surface == ClientSurfaceDesktop {
		return "claude_code_remote"
	}
	return "claude_code_cli"
}

// 作者 long: 自定义请求头让用户在 Agent 身份头之外追加/覆盖任意头，
// 用于某些网关的额外校验（如 x-gateway-token、x-title）。
// 格式：每行一条 "Name: Value"，# 开头的行忽略。

// parseCustomHeaders 解析多行文本形式的自定义请求头。
// 非法行（没有冒号、名称为空、名称含空格或控制字符）直接跳过，
// 保证一个手抖的输入不会让整批探测失败。
func parseCustomHeaders(text string) []clientHeader {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var headers []clientHeader
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || !validHeaderName(name) {
			continue
		}
		headers = append(headers, clientHeader{name: name, value: value})
	}
	return headers
}

// validHeaderName 按 RFC 7230 token 粗略校验头名称。
func validHeaderName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

// applyCustomHeaders 在 Agent 身份头之后覆盖写入自定义头。
// 顺序很重要：放在后面才能"覆盖"预设身份头，例如把 User-Agent 换成别的。
func applyCustomHeaders(req *http.Request, cfg Config) {
	for _, header := range parseCustomHeaders(cfg.CustomHeaders) {
		req.Header.Set(header.name, header.value)
	}
}
