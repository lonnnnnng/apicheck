package main

import (
	"apicheck/internal/checker"
	"fmt"
	"sort"
	"strings"
)

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
	builder.WriteString("\nMODEL                           PROTOCOL              NONSTREAM        STREAM           FIRST TEXT      TOTAL           TOKENS (IN/OUT/CACHED/TOTAL)\n")
	builder.WriteString(strings.Repeat("-", 128) + "\n")
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
		tokens := renderTokens(result.NonStream)
		if result.Stream.Tested && result.NonStream.Tested {
			// 两列都展示时取耗时语义更完整的流式结果；只有非流式时用非流式的。
			tokens = renderTokens(result.Stream)
		} else if result.Stream.Tested {
			tokens = renderTokens(result.Stream)
		}
		fmt.Fprintf(&builder, "%-31s %-21s %-16s %-16s %-15s %-15s %s\n", truncate(result.Model, 31), result.Protocol, non, stream, first, total, tokens)
		for _, message := range probeMessages(result) {
			builder.WriteString(message + "\n")
		}
	}
	fmt.Fprintf(&builder, "\nduration: %dms\n", report.DurationMS)
	return builder.String()
}

// probeMessages 收集失败探测的可读错误信息，以缩进行的形式排在表格行下方。
func probeMessages(result checker.ModelResult) []string {
	probes := []struct {
		label string
		probe checker.ProbeResult
	}{
		{"nonstream", result.NonStream},
		{"stream", result.Stream},
	}
	var messages []string
	for _, entry := range probes {
		if !entry.probe.Tested || entry.probe.OK {
			continue
		}
		message := strings.TrimSpace(entry.probe.ErrorMessage)
		if message == "" {
			continue
		}
		messages = append(messages, fmt.Sprintf("  ↳ %s: %s", entry.label, collapseWhitespace(message)))
	}
	return messages
}

// collapseWhitespace 把多行错误信息压成单行，避免打断表格对齐。
func collapseWhitespace(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", " | ")
	return strings.Join(strings.Fields(text), " ")
}

func renderProbe(result checker.ProbeResult) string {
	if !result.Tested {
		return "-"
	}
	if result.OK {
		if result.Truncated {
			// 模型已正常出字，只是被 max tokens 截断，链路是通的。
			return "ok(截断) " + millis(result.TotalMS, 0)
		}
		return "ok " + millis(result.TotalMS, 0)
	}
	if result.HTTPStatus > 0 {
		return fmt.Sprintf("fail %d/%s", result.HTTPStatus, result.ErrorClass)
	}
	return "fail " + result.ErrorClass
}

// renderTokens 输出 "in/out/cached/total"；usage 全为零说明网关没有返回 usage。
func renderTokens(result checker.ProbeResult) string {
	usage := result.TokenUsage
	if usage.Input == 0 && usage.Output == 0 && usage.Cached == 0 && usage.Total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d/%d/%d", usage.Input, usage.Output, usage.Cached, usage.Total)
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
