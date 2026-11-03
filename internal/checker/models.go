package checker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

func parseModelIDs(body []byte) ([]string, error) {
	var payload struct {
		Data *[]struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Data == nil {
		return nil, errors.New("models response does not contain data[]")
	}
	// 作者 long: 标准模型接口允许 data 为空数组，这表示账号当前没有可用模型；
	// 只有 data 缺失或包含条目却没有任何 id 时，才属于响应结构错误。
	if len(*payload.Data) == 0 {
		return []string{}, nil
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, item := range *payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("models response does not contain data[].id")
	}
	return ids, nil
}

func filterModels(models []string, filter string, max int) []string {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var out []string
	for _, model := range models {
		if filter != "" && !strings.Contains(strings.ToLower(model), filter) {
			continue
		}
		out = append(out, model)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// probeAllModelSources 并行探测所有 root×authStyle 组合（最多 4 个并发），
// 但按 root 顺序、每个 root 内 openai 在 anthropic 之前的固定次序输出，保证多次执行结果稳定可对比。
func probeAllModelSources(ctx context.Context, client *http.Client, cfg Config, roots []string) []ModelSource {
	type probe struct {
		root      string
		authStyle string
	}
	var probes []probe
	for _, root := range roots {
		if shouldProbeOpenAIModels(cfg.Protocol) {
			probes = append(probes, probe{root, "openai"})
		}
		if shouldProbeAnthropicModels(cfg.Protocol) {
			probes = append(probes, probe{root, "anthropic"})
		}
	}
	if len(probes) == 0 {
		return nil
	}
	results := make([]ModelSource, len(probes))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range probes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = listModels(ctx, client, cfg, probes[i].root, probes[i].authStyle)
		}(i)
	}
	wg.Wait()
	return results
}

func shouldProbeOpenAIModels(protocol Protocol) bool {
	switch protocol {
	case ProtocolAuto, ProtocolAll, ProtocolOpenAI, ProtocolOpenAIChat, ProtocolOpenAIResponses:
		return true
	default:
		return false
	}
}

func shouldProbeAnthropicModels(protocol Protocol) bool {
	switch protocol {
	case ProtocolAuto, ProtocolAll, ProtocolAnthropic, ProtocolAnthropicMessages:
		return true
	default:
		return false
	}
}

func listModels(ctx context.Context, client *http.Client, cfg Config, root, authStyle string) ModelSource {
	source := ModelSource{Root: root, AuthStyle: authStyle}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(root, "/models"), nil)
	if err != nil {
		source.Error = err.Error()
		return source
	}
	req.Header.Set("Accept", "application/json")
	applyAuthHeaders(req, cfg.APIKey, authStyle)
	applyClientHeaders(req, authStyle, cfg)
	applyCustomHeaders(req, cfg)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		source.Error = err.Error()
		return source
	}
	defer resp.Body.Close()
	source.HTTPStatus = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		source.Error = err.Error()
		return source
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		source.Error = fmt.Sprintf("%s after %dms", classifyHTTPError(resp.StatusCode, body), elapsedMS(start))
		return source
	}
	models, err := parseModelIDs(body)
	if err != nil {
		source.Error = err.Error()
		return source
	}
	source.Models = filterModels(models, cfg.Filter, cfg.MaxModels)
	return source
}

func applyAuthHeaders(req *http.Request, apiKey, authStyle string) {
	if authStyle == "anthropic" {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
}

func buildTasks(sources []ModelSource, cfg Config) []TaskSpec {
	seen := make(map[string]struct{})
	var tasks []TaskSpec
	add := func(root string, protocol Protocol, models []string) {
		for _, model := range models {
			key := root + "\x00" + string(protocol) + "\x00" + model
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			tasks = append(tasks, TaskSpec{Root: root, Protocol: protocol, Model: model})
		}
	}
	for _, source := range sources {
		if len(source.Models) == 0 {
			continue
		}
		// 同一个模型列表可能来自聚合网关，不能只按鉴权头认定协议；这里保留协议维度，
		// 让后续真实对话请求来确认每个模型到底支持哪条标准端点。
		switch cfg.Protocol {
		case ProtocolOpenAIChat:
			add(source.Root, ProtocolOpenAIChat, source.Models)
		case ProtocolOpenAIResponses:
			add(source.Root, ProtocolOpenAIResponses, source.Models)
		case ProtocolAnthropicMessages:
			add(source.Root, ProtocolAnthropicMessages, source.Models)
		case ProtocolOpenAI:
			add(source.Root, ProtocolOpenAIChat, source.Models)
			add(source.Root, ProtocolOpenAIResponses, source.Models)
		case ProtocolAnthropic:
			add(source.Root, ProtocolAnthropicMessages, source.Models)
		default:
			if source.AuthStyle == "anthropic" {
				add(source.Root, ProtocolAnthropicMessages, source.Models)
			} else {
				add(source.Root, ProtocolOpenAIChat, source.Models)
				add(source.Root, ProtocolOpenAIResponses, source.Models)
			}
		}
	}
	return tasks
}

func normalizeTaskSpecs(tasks []TaskSpec) []TaskSpec {
	seen := make(map[string]struct{})
	out := make([]TaskSpec, 0, len(tasks))
	for _, task := range tasks {
		task.Root = strings.TrimSpace(task.Root)
		task.Model = strings.TrimSpace(task.Model)
		if task.Root == "" || task.Model == "" || task.Protocol == "" {
			continue
		}
		key := task.AccountID + "\x00" + task.Root + "\x00" + string(task.Protocol) + "\x00" + task.Model
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, task)
	}
	return out
}
