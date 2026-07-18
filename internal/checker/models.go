package checker

import (
	"encoding/json"
	"errors"
	"strings"
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
