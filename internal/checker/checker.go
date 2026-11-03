package checker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

const (
	defaultProbePrompt       = "Reply with exactly: pong"
	defaultRequestTimeout    = 60 * time.Second
	defaultConnectTimeout    = 30 * time.Second
	defaultStreamFirstWait   = 60 * time.Second
	defaultStreamIdleTimeout = 60 * time.Second
	defaultStreamMaxDuration = 10 * time.Minute
	// 探测请求的 max tokens。设为 1 也能测通，但真实网关配合长系统提示词时
	// 模型往往写到一半就到上限，回一个"截断"事件，容易让人误判成不可用。
	// 256 足够输出一句完整回答，成本仍然极低。
	defaultProbeMaxTokens       = 256
	nonStreamResponseLimitBytes = 2 << 20 // 非流式响应体读取上限，超出部分丢弃并标记截断。
	streamRawLimitBytes         = 2 << 20 // 流式原始响应累积上限。
	streamTextLimitBytes        = 1 << 20 // 流式文本预览累积上限。
	streamTruncatedMarker       = "\n…[truncated]"
)

func Run(ctx context.Context, cfg Config) (*Report, error) {
	report, err := ListModels(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(report.Tasks) == 0 {
		return report, nil
	}
	checkReport, err := CheckModels(ctx, cfg, report.Tasks)
	if err != nil {
		return nil, err
	}
	report.Results = checkReport.Results
	report.FinishedAt = checkReport.FinishedAt
	report.DurationMS = report.FinishedAt.Sub(report.StartedAt).Milliseconds()
	return report, nil
}

func ListModels(ctx context.Context, cfg Config) (*Report, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("base url is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("api key is required")
	}
	cfg = withDefaults(cfg)
	roots := NormalizeRoots(cfg.BaseURL)
	if len(roots) == 0 {
		return nil, fmt.Errorf("invalid base url: %s", cfg.BaseURL)
	}

	started := time.Now()
	client := newHTTPClient(cfg, cfg.Timeout, 0)
	report := &Report{Roots: roots, Sources: []ModelSource{}, Tasks: []TaskSpec{}, Results: []ModelResult{}, StartedAt: started}
	report.Sources = probeAllModelSources(ctx, client, cfg, roots)
	report.Tasks = buildTasks(report.Sources, cfg)
	report.FinishedAt = time.Now()
	report.DurationMS = report.FinishedAt.Sub(started).Milliseconds()
	return report, nil
}

func CheckModels(ctx context.Context, cfg Config, tasks []TaskSpec) (*Report, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("base url is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("api key is required")
	}
	cfg = withDefaults(cfg)
	roots := NormalizeRoots(cfg.BaseURL)
	if len(roots) == 0 {
		return nil, fmt.Errorf("invalid base url: %s", cfg.BaseURL)
	}
	started := time.Now()
	report := &Report{Roots: roots, Sources: []ModelSource{}, Tasks: normalizeTaskSpecs(tasks), Results: []ModelResult{}, StartedAt: started}
	if len(report.Tasks) > 0 {
		report.Results = runTasks(ctx, cfg, report.Tasks)
	}
	report.FinishedAt = time.Now()
	report.DurationMS = report.FinishedAt.Sub(started).Milliseconds()
	return report, nil
}

func withDefaults(cfg Config) Config {
	if cfg.Protocol == "" {
		cfg.Protocol = ProtocolAuto
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeBoth
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultRequestTimeout
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.StreamFirstTimeout <= 0 {
		cfg.StreamFirstTimeout = defaultStreamFirstWait
	}
	if cfg.StreamIdleTimeout <= 0 {
		cfg.StreamIdleTimeout = defaultStreamIdleTimeout
	}
	if cfg.DisableStreamMax {
		cfg.StreamMaxDuration = 0
	} else if cfg.StreamMaxDuration <= 0 {
		cfg.StreamMaxDuration = defaultStreamMaxDuration
	}
	if strings.TrimSpace(cfg.Prompt) == "" {
		cfg.Prompt = defaultProbePrompt
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultProbeMaxTokens
	}
	cfg.ReasoningEffort = normalizeReasoningEffort(cfg.ReasoningEffort)
	cfg.ClientSurface = normalizeClientSurface(cfg.ClientSurface, ClientSurfaceCLI)
	cfg.OpenAIClientSurface = normalizeClientSurface(cfg.OpenAIClientSurface, cfg.ClientSurface)
	cfg.AnthropicClientSurface = normalizeClientSurface(cfg.AnthropicClientSurface, cfg.ClientSurface)
	return cfg
}

func newHTTPClient(cfg Config, totalTimeout, responseHeaderTimeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   cfg.ConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	if cfg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式测试自签名网关时才会打开。
	}
	applyProxy(transport, cfg.Proxy)
	return &http.Client{Timeout: totalTimeout, Transport: transport}
}

// applyProxy 让探测请求走用户指定的出口代理。
// 单独处理 socks5：http.Transport 的 Proxy 只认 http/https 代理。
func applyProxy(transport *http.Transport, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return
	}
	switch strings.ToLower(parsed.Scheme) {
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(parsed, &net.Dialer{Timeout: 30 * time.Second})
		if err != nil {
			return
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		}
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsed)
	}
}
func runTasks(ctx context.Context, cfg Config, tasks []TaskSpec) []ModelResult {
	onResult := cfg.OnResult
	nonStreamClient := newHTTPClient(cfg, cfg.Timeout, 0)
	streamClient := newHTTPClient(cfg, 0, cfg.StreamFirstTimeout)
	jobs := make(chan modelTask)
	results := make(chan ModelResult)
	workers := cfg.Concurrency
	if workers > len(tasks) {
		workers = len(tasks)
	}
	if workers < 1 {
		workers = 1
	}
	// 作者 long: 自适应并发。被网关限流时把"同时进行"的请求数上限压下来，
	// 已启动的 worker 不退出，只是排队等待许可，避免越试越限。
	var permits atomic.Int64
	permits.Store(int64(workers))
	if cfg.AdaptiveConcurrency {
		// 作者 long: 起步先给一半窗口，而不是从 1 开始串行爬坡。
		// 从 1 起步时，前几个请求总是单跑，整批检测的墙钟时间被拖长一倍；
		// 一半窗口仍能在真正撞限流时迅速收敛，又能立刻用上并发。
		permits.Store(initialPermits(workers))
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				if cfg.AdaptiveConcurrency {
					acquirePermit(&permits)
				}
				result, throttled := runTaskWithRetry(ctx, nonStreamClient, streamClient, cfg, task)
				if cfg.AdaptiveConcurrency {
					if throttled {
						halvePermits(&permits, workers)
					} else {
						permits.Add(1)
						if permits.Load() > int64(workers) {
							permits.Store(int64(workers))
						}
					}
				}
				results <- result
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, task := range tasks {
			job := modelTask{accountID: task.AccountID, root: task.Root, protocol: task.Protocol, model: task.Model}
			select {
			case <-ctx.Done():
				return
			case jobs <- job:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	out := make([]ModelResult, 0, len(tasks))
	for result := range results {
		out = append(out, result)
		if onResult != nil {
			onResult(result)
		}
	}
	// 按完成序收结果会让同一批次的行序随机波动，排序后保证多次执行可对比。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		if out[i].Root != out[j].Root {
			return out[i].Root < out[j].Root
		}
		return out[i].AccountID < out[j].AccountID
	})
	return out
}

func runTask(ctx context.Context, nonStreamClient, streamClient *http.Client, cfg Config, task modelTask) ModelResult {
	result := ModelResult{AccountID: task.accountID, Root: task.root, Protocol: task.protocol, Model: task.model}
	if cfg.Mode == ModeBoth || cfg.Mode == ModeNonStream {
		result.NonStream = probeConversation(ctx, nonStreamClient, cfg, task, false)
	}
	if cfg.Mode == ModeBoth || cfg.Mode == ModeStream {
		result.Stream = probeConversation(ctx, streamClient, cfg, task, true)
	}
	return result
}
