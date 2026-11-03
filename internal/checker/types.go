package checker

import "time"

type Protocol string

const (
	ProtocolAuto              Protocol = "auto"
	ProtocolAll               Protocol = "all"
	ProtocolOpenAI            Protocol = "openai"
	ProtocolOpenAIChat        Protocol = "openai-chat"
	ProtocolOpenAIResponses   Protocol = "openai-responses"
	ProtocolAnthropic         Protocol = "anthropic"
	ProtocolAnthropicMessages Protocol = "anthropic-messages"
)

type Mode string

const (
	ModeBoth      Mode = "both"
	ModeNonStream Mode = "nonstream"
	ModeStream    Mode = "stream"
)

type ClientSurface string

const (
	ClientSurfaceCLI     ClientSurface = "cli"
	ClientSurfaceDesktop ClientSurface = "desktop"
)

// 模型等级（推理强度）取值；与 OpenAI 的 reasoning_effort 枚举一致。
const (
	ReasoningEffortMinimal = "minimal"
	ReasoningEffortLow     = "low"
	ReasoningEffortMedium  = "medium"
	ReasoningEffortHigh    = "high"
)

type Config struct {
	BaseURL string
	APIKey  string
	Prompt  string
	// SystemPromptPresetID 选择系统提示词预设（见 system_prompt.go）；空或 "none" 表示不发送 system prompt。
	SystemPromptPresetID string
	// MaxTokens 是探测请求的 max_output_tokens / max_tokens 上限；<=0 用 defaultProbeMaxTokens。
	MaxTokens int
	// ReasoningEffort 是"模型等级"（推理强度），空或 "none" 表示不发送。
	// 合法值：minimal / low / medium / high。三个协议的字段名不同：
	// openai-chat 用 reasoning_effort，openai-responses 用 reasoning.effort，
	// anthropic-messages 用 output_config.effort（Anthropic 只认 low/medium/high，minimal 会降级成 low）。
	ReasoningEffort string
	// Proxy 是出口代理，支持 http/https/socks5；空表示直连。
	Proxy string
	// CustomHeaders 是用户追加的自定义请求头，每行一条 "Name: Value"。
	// 在 Agent 身份头之后应用，因此可以覆盖预设身份头。
	CustomHeaders string
	// Retries 是单个探测失败后的额外重试次数（仅对限流/5xx/网络类错误重试）。
	Retries int
	// AdaptiveConcurrency 打开后遇到 429 会把并发上限减半，避免越试越限。
	AdaptiveConcurrency    bool
	Protocol               Protocol
	Mode                   Mode
	Concurrency            int
	Timeout                time.Duration
	ConnectTimeout         time.Duration
	StreamFirstTimeout     time.Duration
	StreamIdleTimeout      time.Duration
	StreamMaxDuration      time.Duration
	DisableStreamMax       bool
	Filter                 string
	MaxModels              int
	InsecureSkipVerify     bool
	ClientSurface          ClientSurface
	OpenAIClientSurface    ClientSurface
	AnthropicClientSurface ClientSurface
	Progress               func(ProgressEvent)
}

type ProgressEvent struct {
	Root     string   `json:"root"`
	Protocol Protocol `json:"protocol"`
	Model    string   `json:"model"`
	Mode     Mode     `json:"mode"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text,omitempty"`
	Raw      string   `json:"raw,omitempty"`
}

type Report struct {
	Roots      []string      `json:"roots"`
	Sources    []ModelSource `json:"sources"`
	Tasks      []TaskSpec    `json:"tasks,omitempty"`
	Results    []ModelResult `json:"results"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	DurationMS int64         `json:"duration_ms"`
}

type ModelSource struct {
	Root       string   `json:"root"`
	AuthStyle  string   `json:"auth_style"`
	Models     []string `json:"models"`
	HTTPStatus int      `json:"http_status,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type ModelResult struct {
	AccountID string      `json:"account_id,omitempty"`
	Root      string      `json:"root"`
	Protocol  Protocol    `json:"protocol"`
	Model     string      `json:"model"`
	NonStream ProbeResult `json:"nonstream,omitempty"`
	Stream    ProbeResult `json:"stream,omitempty"`
}

type ProbeResult struct {
	Tested       bool   `json:"tested"`
	OK           bool   `json:"ok"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	ErrorClass   string `json:"error_class,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	FirstByteMS  int64  `json:"first_byte_ms,omitempty"`
	FirstTextMS  int64  `json:"first_text_ms,omitempty"`
	TotalMS      int64  `json:"total_ms,omitempty"`
	Preview      string `json:"preview,omitempty"`
	RawResponse  string `json:"raw_response,omitempty"`
	RawError     string `json:"raw_error,omitempty"`
	// TokenUsage 是从响应 usage 字段提取的 token 用量；网关未返回时为零值，前端不展示。
	TokenUsage TokenUsage `json:"token_usage,omitempty"`
	// Truncated 表示上游明确告知"响应被 max_output_tokens 截断"（responses 的
	// response.incomplete / anthropic 的 stop_reason=max_tokens）。这类响应模型
	// 已经正常出字，说明链路是通的，不应按失败处理。
	Truncated bool `json:"truncated,omitempty"`
	// IncompleteReason 记录截断原因，便于前端提示"把 max tokens 调大"。
	IncompleteReason string `json:"incomplete_reason,omitempty"`
}

// TokenUsage 统一三种协议的 usage 命名：OpenAI 的 prompt/completion tokens
// 与 Anthropic 的 input/output tokens 归为同一组字段；缓存 token 取两家各自的
// 缓存命中或缓存写入（OpenAI cached_tokens / Anthropic cache_read+cache_creation）。
// Total 在上游返回 total_tokens 时直接采用，否则按输入+输出推算。
type TokenUsage struct {
	Input      int64 `json:"input,omitempty"`
	Output     int64 `json:"output,omitempty"`
	Cached     int64 `json:"cached,omitempty"`
	Total      int64 `json:"total,omitempty"`
	FromStream bool  `json:"from_stream,omitempty"`
}

type TaskSpec struct {
	// AccountID 由前端多账号模式透传，只用于把结果归回对应账号；为空时按单账号处理。
	AccountID string   `json:"account_id,omitempty"`
	Root      string   `json:"root"`
	Protocol  Protocol `json:"protocol"`
	Model     string   `json:"model"`
}

type modelTask struct {
	accountID string
	root      string
	protocol  Protocol
	model     string
}
