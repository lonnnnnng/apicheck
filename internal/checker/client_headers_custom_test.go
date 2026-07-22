package checker

import (
	"strings"
	"testing"
)

func TestClientHeadersUseCustomIdentityByProtocol(t *testing.T) {
	openAI := clientHeaderMap(clientHeaders("openai", withDefaults(Config{
		ClientSurface:    ClientSurfaceDesktop,
		OpenAIUserAgent:  "gateway-openai/9.1",
		OpenAIOriginator: "gateway_desktop",
	})))
	if got := openAI["User-Agent"]; got != "gateway-openai/9.1" {
		t.Fatalf("OpenAI User-Agent = %q", got)
	}
	if got := openAI["originator"]; got != "gateway_desktop" {
		t.Fatalf("OpenAI originator = %q", got)
	}

	anthropic := clientHeaderMap(clientHeaders("anthropic", withDefaults(Config{
		ClientSurface:           ClientSurfaceDesktop,
		AnthropicUserAgent:      "gateway-anthropic/4.2",
		AnthropicClientPlatform: "gateway_remote",
	})))
	if got := anthropic["User-Agent"]; got != "gateway-anthropic/4.2" {
		t.Fatalf("Anthropic User-Agent = %q", got)
	}
	if got := anthropic["anthropic-client-platform"]; got != "gateway_remote" {
		t.Fatalf("Anthropic client platform = %q", got)
	}
}

func TestClientHeadersDeriveOpenAIUserAgentFromCustomOriginator(t *testing.T) {
	headers := clientHeaderMap(clientHeaders("openai", withDefaults(Config{
		ClientSurface:    ClientSurfaceDesktop,
		OpenAIOriginator: "custom_origin",
	})))
	if got := headers["User-Agent"]; !strings.HasPrefix(got, "custom_origin/"+codexClientVersion+" ") {
		t.Fatalf("OpenAI User-Agent = %q", got)
	}
}

func TestClientHeadersSanitizeCustomValues(t *testing.T) {
	headers := clientHeaderMap(clientHeaders("openai", withDefaults(Config{
		ClientSurface:    ClientSurfaceDesktop,
		OpenAIUserAgent:  "custom\r\nInjected: yes",
		OpenAIOriginator: "  custom originator  ",
	})))
	if got := headers["User-Agent"]; got != "custom__Injected: yes" {
		t.Fatalf("sanitized OpenAI User-Agent = %q", got)
	}
	if got := headers["originator"]; got != "custom originator" {
		t.Fatalf("sanitized OpenAI originator = %q", got)
	}
}

func clientHeaderMap(headers []clientHeader) map[string]string {
	values := make(map[string]string, len(headers))
	for _, header := range headers {
		values[header.name] = header.value
	}
	return values
}
