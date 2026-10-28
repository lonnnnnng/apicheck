package checker

import "net/http"

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
