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

type Config struct {
	BaseURL                 string
	APIKey                  string
	Prompt                  string
	Protocol                Protocol
	Mode                    Mode
	Concurrency             int
	Timeout                 time.Duration
	ConnectTimeout          time.Duration
	StreamFirstTimeout      time.Duration
	StreamIdleTimeout       time.Duration
	StreamMaxDuration       time.Duration
	DisableStreamMax        bool
	Filter                  string
	MaxModels               int
	InsecureSkipVerify      bool
	ClientSurface           ClientSurface
	OpenAIClientSurface     ClientSurface
	AnthropicClientSurface  ClientSurface
	OpenAIUserAgent         string
	OpenAIOriginator        string
	AnthropicUserAgent      string
	AnthropicClientPlatform string
	Progress                func(ProgressEvent)
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
}

type TaskSpec struct {
	Root     string   `json:"root"`
	Protocol Protocol `json:"protocol"`
	Model    string   `json:"model"`
}

type modelTask struct {
	root     string
	protocol Protocol
	model    string
}
