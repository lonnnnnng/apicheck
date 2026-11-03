package main

import (
	"apicheck/internal/apireq"
	"apicheck/internal/checker"
	"apicheck/internal/render"
	"encoding/json"
	"fmt"
	"net/http"
)

type textRequest struct {
	Text string `json:"text"`
}

func newServerHandler(staticDir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req apireq.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		report, err := checker.ListModels(r.Context(), apireq.ConfigFrom(req, checker.ClientSurfaceCLI))
		writeJSONReport(w, report, err)
	})
	// 作者 long: 批量检测动辄上百个模型，一次性返回会让前端长时间没有任何反馈。
	// 这里用 SSE 把每个模型的结果立刻推回去，前端边收边渲染。
	mux.HandleFunc("/api/check/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		var req apireq.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		writeEvent := func(name string, payload any) {
			data, err := json.Marshal(payload)
			if err != nil {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
			flusher.Flush()
		}
		cfg := apireq.ConfigFrom(req, checker.ClientSurfaceCLI)
		cfg.OnResult = func(result checker.ModelResult) { writeEvent("result", result) }
		var report *checker.Report
		var err error
		if len(req.Tasks) > 0 {
			report, err = checker.CheckModels(r.Context(), cfg, req.Tasks)
		} else {
			report, err = checker.Run(r.Context(), cfg)
		}
		if err != nil {
			writeEvent("error", map[string]string{"error": err.Error()})
			return
		}
		writeEvent("done", report)
	})

	mux.HandleFunc("/api/check", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req apireq.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cfg := apireq.ConfigFrom(req, checker.ClientSurfaceCLI)
		var report *checker.Report
		var err error
		if len(req.Tasks) > 0 {
			report, err = checker.CheckModels(r.Context(), cfg, req.Tasks)
		} else {
			report, err = checker.Run(r.Context(), cfg)
		}
		writeJSONReport(w, report, err)
	})
	mux.HandleFunc("/api/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var payload apireq.PreviewPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cfg := apireq.ConfigFrom(payload.Request, checker.ClientSurfaceCLI)
		kind := apireq.ParsePreviewKind(payload.Kind)
		var text string
		var err error
		if len(payload.Tasks) > 0 {
			text, err = checker.Preview(cfg, payload.Tasks[0], payload.Stream, kind)
		} else {
			text, err = checker.ModelListPreview(cfg, kind)
		}
		writeJSONValue(w, map[string]string{"preview": text}, err)
	})
	mux.HandleFunc("/api/system-prompts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSONValue(w, checker.SystemPromptPresets(), nil)
	})
	mux.HandleFunc("/api/agent-identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			SystemPromptPreset string `json:"system_prompt_preset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		preset, identity, ok := checker.AgentIdentityForPreset(req.SystemPromptPreset)
		if !ok {
			http.Error(w, "unknown preset", http.StatusNotFound)
			return
		}
		writeJSONValue(w, map[string]any{"preset": preset, "identity": identity}, nil)
	})
	mux.HandleFunc("/api/markdown", func(w http.ResponseWriter, r *http.Request) {
		var req textRequest
		if !decodeJSONRequest(w, r, &req) {
			return
		}
		html, err := render.RenderMarkdown(req.Text)
		writeJSONValue(w, map[string]string{"html": html}, err)
	})
	mux.HandleFunc("/api/qrcode", func(w http.ResponseWriter, r *http.Request) {
		var req textRequest
		if !decodeJSONRequest(w, r, &req) {
			return
		}
		dataURL, err := render.GenerateQRCodeDataURL(req.Text)
		writeJSONValue(w, map[string]string{"data_url": dataURL}, err)
	})
	return mux
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSONValue(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONReport(w http.ResponseWriter, report *checker.Report, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(report)
}
