package checker

import (
	"encoding/json"
	"errors"
	"strings"
)

func parseModelIDs(body []byte) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, item := range payload.Data {
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
