package checker

import (
	"net/http"
	"strings"
)

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
