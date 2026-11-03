package main

import (
	"apicheck/internal/apireq"
	"apicheck/internal/checker"
	"apicheck/internal/version"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	var cfg checker.Config
	var format string
	var output string
	var serve bool
	var listen string
	var staticDir string
	var streamMaxDuration string
	var strict bool
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&cfg.BaseURL, "base-url", "", "API root URL, for example https://example.com/v1")
	flag.StringVar(&cfg.APIKey, "api-key", "", "API key, can also be provided by APICHECK_API_KEY")
	flag.Var((*protocolFlag)(&cfg.Protocol), "protocol", "auto/openai/openai-chat/openai-responses/anthropic/anthropic-messages/all")
	flag.Var((*modeFlag)(&cfg.Mode), "mode", "both/nonstream/stream")
	flag.IntVar(&cfg.Concurrency, "concurrency", 2, "model probe concurrency")
	flag.DurationVar(&cfg.Timeout, "timeout", 60*time.Second, "single request timeout")
	flag.DurationVar(&cfg.ConnectTimeout, "connect-timeout", 30*time.Second, "TCP/TLS connect timeout")
	flag.DurationVar(&cfg.StreamFirstTimeout, "stream-first-timeout", 60*time.Second, "stream first packet timeout")
	flag.DurationVar(&cfg.StreamIdleTimeout, "stream-idle-timeout", 60*time.Second, "stream idle timeout")
	flag.StringVar(&streamMaxDuration, "stream-max-duration", "10m", "stream max duration, 0 means unlimited")
	flag.StringVar(&cfg.Prompt, "prompt", "", "conversation probe prompt")
	flag.IntVar(&cfg.MaxTokens, "max-tokens", 0, "max output tokens for the probe request (default 256)")
	flag.StringVar(&cfg.ReasoningEffort, "reasoning-effort", "", "模型等级（推理强度）：minimal|low|medium|high，留空不发送")
	flag.StringVar(&cfg.Proxy, "proxy", "", "outbound proxy, for example http://127.0.0.1:8080 or socks5://127.0.0.1:1080")
	flag.StringVar(&cfg.CustomHeaders, "header", "", "extra request headers, one 'Name: Value' per line")
	flag.IntVar(&cfg.Retries, "retries", 0, "extra retries on rate limit / 5xx / network errors")
	flag.BoolVar(&cfg.AdaptiveConcurrency, "adaptive-concurrency", false, "halve probe concurrency when the gateway rate limits")
	flag.StringVar(&cfg.SystemPromptPresetID, "system-prompt", "", "system prompt preset: "+strings.Join(checker.SystemPromptIDs(), "/"))
	flag.StringVar(&cfg.Filter, "filter", "", "only test models containing this text")
	flag.IntVar(&cfg.MaxModels, "max-models", 0, "max models per discovered source, 0 means unlimited")
	flag.BoolVar(&cfg.InsecureSkipVerify, "insecure-skip-verify", false, "skip TLS verification for self-signed gateways")
	flag.Var((*surfaceFlag)(&cfg.OpenAIClientSurface), "openai-ua-surface", "cli/desktop User-Agent surface for OpenAI-compatible requests")
	flag.Var((*surfaceFlag)(&cfg.AnthropicClientSurface), "anthropic-ua-surface", "cli/desktop User-Agent surface for Anthropic-compatible requests")
	flag.BoolVar(&strict, "strict", false, "exit with code 1 when any tested probe fails")
	flag.StringVar(&format, "format", "table", "table/json")
	flag.StringVar(&output, "output", "", "write output to file")
	flag.BoolVar(&serve, "serve", false, "start local web UI server")
	flag.StringVar(&listen, "listen", "127.0.0.1:8787", "web UI listen address")
	flag.StringVar(&staticDir, "static-dir", "ui", "directory that contains index.html")
	flag.Parse()
	if showVersion {
		fmt.Println(version.Version)
		return
	}
	cfg.StreamMaxDuration, cfg.DisableStreamMax = apireq.ParseStreamMaxDuration(streamMaxDuration, 10*time.Minute)

	if serve {
		if err := runServer(listen, staticDir); err != nil {
			fmt.Fprintln(os.Stderr, "apicheck:", err)
			os.Exit(1)
		}
		return
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("APICHECK_API_KEY")
	}
	report, err := checker.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apicheck:", err)
		os.Exit(1)
	}

	var data []byte
	switch strings.ToLower(format) {
	case "json":
		data, err = json.MarshalIndent(report, "", "  ")
	default:
		data = []byte(renderTable(report))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "apicheck:", err)
		os.Exit(1)
	}
	if output != "" {
		if err := os.WriteFile(output, append(data, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "apicheck:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Println(string(data))
	if strict && reportHasFailures(report) {
		os.Exit(1)
	}
}

// reportHasFailures 报告是否存在已执行且失败的探测，供 --strict 判定退出码。
func reportHasFailures(report *checker.Report) bool {
	for _, result := range report.Results {
		for _, probe := range []checker.ProbeResult{result.NonStream, result.Stream} {
			if probe.Tested && !probe.OK {
				return true
			}
		}
	}
	return false
}
