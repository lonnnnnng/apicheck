package presentation

import (
	"strings"
	"testing"
)

func TestRenderMarkdownSupportsCommonGFM(t *testing.T) {
	source := "# 标题\n\n**粗体** 和 ~~删除线~~\n\n- 条目\n- [x] 已完成\n\n| 名称 | 状态 |\n| --- | --- |\n| API | 可用 |\n\n```go\nfmt.Println(\"ok\")\n```\n\n[官网](https://example.com)"

	html, err := RenderMarkdown(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<h1>标题</h1>",
		"<strong>粗体</strong>",
		"<del>删除线</del>",
		"<ul>",
		`type="checkbox"`,
		"<table>",
		`class="language-go"`,
		`href="https://example.com"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered markdown missing %q:\n%s", want, html)
		}
	}
}

func TestRenderMarkdownSanitizesUnsafeContent(t *testing.T) {
	html, err := RenderMarkdown("<script>alert(1)</script>\n\n[危险链接](javascript:alert(1))")
	if err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []string{"<script", "javascript:", "onclick="} {
		if strings.Contains(strings.ToLower(html), blocked) {
			t.Fatalf("unsafe content %q remained in:\n%s", blocked, html)
		}
	}
}
