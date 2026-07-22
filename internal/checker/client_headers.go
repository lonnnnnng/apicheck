package checker

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

const (
	codexClientVersion      = "0.142.5"
	claudeCodeClientVersion = "2.1.198"
)

type clientHeader struct {
	name  string
	value string
}

var osVersionCache struct {
	once  sync.Once
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

func clientHeaders(authStyle string, cfg Config) []clientHeader {
	surface := cfg.clientSurfaceForAuthStyle(authStyle)
	if authStyle == "anthropic" {
		return []clientHeader{
			{name: "User-Agent", value: configuredClientHeaderValue(cfg.AnthropicUserAgent, claudeCodeUserAgent())},
			{name: "anthropic-client-platform", value: configuredClientHeaderValue(cfg.AnthropicClientPlatform, claudeCodeClientPlatform(surface))},
		}
	}
	originator := configuredClientHeaderValue(cfg.OpenAIOriginator, codexOriginator(surface))
	return []clientHeader{
		{name: "User-Agent", value: configuredClientHeaderValue(cfg.OpenAIUserAgent, codexUserAgent(originator))},
		{name: "originator", value: originator},
	}
}

func configuredClientHeaderValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	// 作者 long: 自定义标识会直接进入 HTTP Header，沿用现有 ASCII 清洗可阻断换行注入并避免 Go 拒绝非法字节。
	if value = strings.TrimSpace(sanitizeHeaderValue(value)); value == "" {
		return fallback
	}
	return value
}

func codexUserAgent(originator string) string {
	// 作者 long: Codex 官方客户端会把来源、版本、系统、架构和终端标识拼进 UA；聚合网关常按这个结构放行 Codex 流量。
	return sanitizeHeaderValue(fmt.Sprintf("%s/%s (%s %s; %s) %s", originator, codexClientVersion, osName(), osVersion(), osArch(), terminalUserAgentToken()))
}

func codexOriginator(surface ClientSurface) string {
	if surface == ClientSurfaceDesktop {
		return "codex_chatgpt_desktop"
	}
	return "codex_cli_rs"
}

func claudeCodeUserAgent() string {
	// 作者 long: Claude Code native 包的 Anthropic API 请求使用 claude-code/<version>，不是 “Claude Code Desktop” 这类展示名。
	return "claude-code/" + claudeCodeClientVersion
}

func claudeCodeClientPlatform(surface ClientSurface) string {
	if surface == ClientSurfaceDesktop {
		return "claude_code_remote"
	}
	return "claude_code_cli"
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

func osVersion() string {
	osVersionCache.once.Do(func() {
		osVersionCache.value = "unknown"
		if runtime.GOOS == "darwin" {
			out, err := exec.Command("sw_vers", "-productVersion").Output()
			if err == nil {
				if value := strings.TrimSpace(string(out)); value != "" {
					osVersionCache.value = value
				}
			}
		}
	})
	return osVersionCache.value
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

func terminalUserAgentToken() string {
	program := strings.TrimSpace(os.Getenv("TERM_PROGRAM"))
	version := strings.TrimSpace(os.Getenv("TERM_PROGRAM_VERSION"))
	if program != "" && version != "" {
		return sanitizeToken(program + "/" + version)
	}
	if program != "" {
		return sanitizeToken(program)
	}
	if term := strings.TrimSpace(os.Getenv("TERM")); term != "" {
		return sanitizeToken(term)
	}
	return "unknown"
}

func sanitizeToken(value string) string {
	value = sanitizeHeaderValue(value)
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, ";", "_")
	value = strings.ReplaceAll(value, "(", "_")
	value = strings.ReplaceAll(value, ")", "_")
	if value == "" {
		return "unknown"
	}
	return value
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
