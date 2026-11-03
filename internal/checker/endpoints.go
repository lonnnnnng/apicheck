package checker

import (
	"net/url"
	"strings"
)

var knownEndpointSuffixes = []string{
	"/chat/completions",
	"/models",
	"/responses",
	"/messages",
}

func NormalizeRoots(raw string) []string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return nil
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.EscapedPath(), "/")
	if parsed.Path == "" {
		parsed.Path = ""
	}
	for _, suffix := range knownEndpointSuffixes {
		if strings.HasSuffix(parsed.Path, suffix) {
			parsed.Path = strings.TrimRight(strings.TrimSuffix(parsed.Path, suffix), "/")
			break
		}
	}

	var roots []string
	add := func(u url.URL) {
		text := strings.TrimRight(u.String(), "/")
		for _, existing := range roots {
			if existing == text {
				return
			}
		}
		roots = append(roots, text)
	}
	add(*parsed)
	if !strings.HasSuffix(parsed.Path, "/v1") {
		withV1 := *parsed
		withV1.Path = strings.TrimRight(parsed.Path, "/") + "/v1"
		if parsed.Path == "" {
			withV1.Path = "/v1"
		}
		add(withV1)
	}
	return roots
}

func joinURL(root, endpoint string) string {
	return strings.TrimRight(root, "/") + endpoint
}
