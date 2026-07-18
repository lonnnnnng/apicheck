package presentation

import (
	"bytes"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var markdownRenderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

var markdownPolicy = newMarkdownPolicy()

func newMarkdownPolicy() *bluemonday.Policy {
	policy := bluemonday.UGCPolicy()
	// 作者 long: Goldmark 的任务列表会生成禁用 checkbox，这里只放行不可交互的任务状态，
	// 同时限制代码语言 class，避免模型输出借助任意属性影响应用界面或执行脚本。
	policy.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	policy.AllowAttrs("checked", "disabled").OnElements("input")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[A-Za-z0-9_-]+$`)).OnElements("code")
	return policy
}

func RenderMarkdown(source string) (string, error) {
	var rendered bytes.Buffer
	if err := markdownRenderer.Convert([]byte(source), &rendered); err != nil {
		return "", err
	}
	return markdownPolicy.Sanitize(rendered.String()), nil
}
