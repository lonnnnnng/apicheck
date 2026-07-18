package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"apicheck/internal/checker"
	"apicheck/internal/presentation"
)

func main() {
	var cfg checker.Config
	var format string
	var output string
	var serve bool
	var listen string
	var staticDir string
	var streamMaxDuration string
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
	flag.StringVar(&cfg.Filter, "filter", "", "only test models containing this text")
	flag.IntVar(&cfg.MaxModels, "max-models", 0, "max models per discovered source, 0 means unlimited")
	flag.BoolVar(&cfg.InsecureSkipVerify, "insecure-skip-verify", false, "skip TLS verification for self-signed gateways")
	flag.Var((*surfaceFlag)(&cfg.OpenAIClientSurface), "openai-ua-surface", "cli/desktop User-Agent surface for OpenAI-compatible requests")
	flag.Var((*surfaceFlag)(&cfg.AnthropicClientSurface), "anthropic-ua-surface", "cli/desktop User-Agent surface for Anthropic-compatible requests")
	flag.StringVar(&format, "format", "table", "table/json")
	flag.StringVar(&output, "output", "", "write output to file")
	flag.BoolVar(&serve, "serve", false, "start local web UI server")
	flag.StringVar(&listen, "listen", "127.0.0.1:8787", "web UI listen address")
	flag.StringVar(&staticDir, "static-dir", "prototype", "directory that contains index.html")
	flag.Parse()
	cfg.StreamMaxDuration, cfg.DisableStreamMax = parseStreamMaxDuration(streamMaxDuration, 10*time.Minute)

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
}

type checkRequest struct {
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
	Tasks              []checker.TaskSpec `json:"tasks"`
}

type textRequest struct {
	Text string `json:"text"`
}

func runServer(listen, staticDir string) error {
	fmt.Printf("API Checker web UI: http://%s\n", listen)
	return http.ListenAndServe(listen, newServerHandler(staticDir))
}

func newServerHandler(staticDir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req checkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		report, err := checker.ListModels(r.Context(), configFromRequest(req))
		writeJSONReport(w, report, err)
	})
	mux.HandleFunc("/api/check", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req checkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cfg := configFromRequest(req)
		var report *checker.Report
		var err error
		if len(req.Tasks) > 0 {
			report, err = checker.CheckModels(r.Context(), cfg, req.Tasks)
		} else {
			report, err = checker.Run(r.Context(), cfg)
		}
		writeJSONReport(w, report, err)
	})
	mux.HandleFunc("/api/markdown", func(w http.ResponseWriter, r *http.Request) {
		var req textRequest
		if !decodeJSONRequest(w, r, &req) {
			return
		}
		html, err := presentation.RenderMarkdown(req.Text)
		writeJSONValue(w, map[string]string{"html": html}, err)
	})
	mux.HandleFunc("/api/qrcode", func(w http.ResponseWriter, r *http.Request) {
		var req textRequest
		if !decodeJSONRequest(w, r, &req) {
			return
		}
		dataURL, err := presentation.GenerateQRCodeDataURL(req.Text)
		writeJSONValue(w, map[string]string{"data_url": dataURL}, err)
	})
	return mux
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSONValue(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func configFromRequest(req checkRequest) checker.Config {
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
		OpenAIClientSurface:    parseClientSurfaceOrDefault(req.OpenAIUASurface, checker.ClientSurfaceCLI),
		AnthropicClientSurface: parseClientSurfaceOrDefault(req.AnthropicUASurface, checker.ClientSurfaceCLI),
	}
}

func writeJSONReport(w http.ResponseWriter, report *checker.Report, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(report)
}

type protocolFlag checker.Protocol

func (p *protocolFlag) String() string {
	if *p == "" {
		return string(checker.ProtocolAuto)
	}
	return string(*p)
}

func (p *protocolFlag) Set(value string) error {
	protocol := checker.Protocol(strings.ToLower(strings.TrimSpace(value)))
	switch protocol {
	case checker.ProtocolAuto, checker.ProtocolAll, checker.ProtocolOpenAI, checker.ProtocolOpenAIChat, checker.ProtocolOpenAIResponses, checker.ProtocolAnthropic, checker.ProtocolAnthropicMessages:
		*p = protocolFlag(protocol)
		return nil
	default:
		return fmt.Errorf("unsupported protocol %q", value)
	}
}

type modeFlag checker.Mode

func (m *modeFlag) String() string {
	if *m == "" {
		return string(checker.ModeBoth)
	}
	return string(*m)
}

func (m *modeFlag) Set(value string) error {
	mode := checker.Mode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case checker.ModeBoth, checker.ModeNonStream, checker.ModeStream:
		*m = modeFlag(mode)
		return nil
	default:
		return fmt.Errorf("unsupported mode %q", value)
	}
}

type surfaceFlag checker.ClientSurface

func (s *surfaceFlag) String() string {
	if *s == "" {
		return string(checker.ClientSurfaceCLI)
	}
	return string(*s)
}

func (s *surfaceFlag) Set(value string) error {
	surface := checker.ClientSurface(strings.ToLower(strings.TrimSpace(value)))
	switch surface {
	case checker.ClientSurfaceCLI, checker.ClientSurfaceDesktop:
		*s = surfaceFlag(surface)
		return nil
	default:
		return fmt.Errorf("unsupported client surface %q", value)
	}
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

func renderTable(report *checker.Report) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "roots: %s\n", strings.Join(report.Roots, ", "))
	for _, source := range report.Sources {
		status := "ok"
		if source.Error != "" {
			status = source.Error
		}
		fmt.Fprintf(&builder, "models[%s %s]: %d (%s)\n", source.AuthStyle, source.Root, len(source.Models), status)
	}
	if len(report.Results) == 0 {
		builder.WriteString("\nno model probe tasks generated\n")
		return builder.String()
	}
	builder.WriteString("\nMODEL                           PROTOCOL              NONSTREAM        STREAM           FIRST TEXT      TOTAL\n")
	builder.WriteString(strings.Repeat("-", 112) + "\n")
	results := append([]checker.ModelResult(nil), report.Results...)
	sort.Slice(results, func(i, j int) bool {
		if results[i].Model == results[j].Model {
			return results[i].Protocol < results[j].Protocol
		}
		return results[i].Model < results[j].Model
	})
	for _, result := range results {
		non := renderProbe(result.NonStream)
		stream := renderProbe(result.Stream)
		first := "-"
		total := "-"
		if result.Stream.Tested {
			first = millis(result.Stream.FirstTextMS, result.Stream.FirstByteMS)
			total = millis(result.Stream.TotalMS, 0)
		} else if result.NonStream.Tested {
			first = millis(result.NonStream.FirstByteMS, 0)
			total = millis(result.NonStream.TotalMS, 0)
		}
		fmt.Fprintf(&builder, "%-31s %-21s %-16s %-16s %-15s %s\n", truncate(result.Model, 31), result.Protocol, non, stream, first, total)
	}
	fmt.Fprintf(&builder, "\nduration: %dms\n", report.DurationMS)
	return builder.String()
}

func renderProbe(result checker.ProbeResult) string {
	if !result.Tested {
		return "-"
	}
	if result.OK {
		return "ok " + millis(result.TotalMS, 0)
	}
	if result.HTTPStatus > 0 {
		return fmt.Sprintf("fail %d/%s", result.HTTPStatus, result.ErrorClass)
	}
	return "fail " + result.ErrorClass
}

func millis(primary, fallback int64) string {
	if primary > 0 {
		return fmt.Sprintf("%dms", primary)
	}
	if fallback > 0 {
		return fmt.Sprintf("%dms", fallback)
	}
	return "-"
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	if max <= 1 {
		return value[:max]
	}
	return value[:max-1] + "…"
}
