package checker

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplyProxyConfiguresTransport(t *testing.T) {
	transport := &http.Transport{}
	applyProxy(transport, "http://127.0.0.1:8080")
	if transport.Proxy == nil {
		t.Fatal("http 代理应设置 Transport.Proxy")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	url, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if url == nil || url.Host != "127.0.0.1:8080" {
		t.Fatalf("代理地址异常: %v", url)
	}
}

func TestApplyProxySocks5ReplacesDialer(t *testing.T) {
	transport := &http.Transport{}
	applyProxy(transport, "socks5://127.0.0.1:1080")
	if transport.Proxy != nil {
		t.Fatal("socks5 不应走 Transport.Proxy")
	}
	if transport.DialContext == nil {
		t.Fatal("socks5 应替换 DialContext")
	}
}

// 空值 / 非法值不应该影响直连。
func TestApplyProxyIgnoresInvalid(t *testing.T) {
	for _, raw := range []string{"", "   ", "not a url", "ftp://x"} {
		transport := &http.Transport{}
		applyProxy(transport, raw)
		if transport.Proxy != nil {
			t.Fatalf("%q 不应设置代理", raw)
		}
	}
}

// 走代理时 /models 请求也要发到代理上（这里用一个假的 http 代理验证）。
func TestListModelsGoesThroughProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer backend.Close()

	var proxied int
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只处理绝对 URI 的转发请求，其余当作普通代理请求计数。
		proxied++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"from-proxy"}]}`))
	}))
	defer proxySrv.Close()

	cfg := Config{BaseURL: backend.URL, APIKey: "k", Protocol: ProtocolOpenAI, Proxy: proxySrv.URL}
	report, err := ListModels(t.Context(), cfg)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if proxied == 0 {
		t.Fatal("请求未经过代理")
	}
	found := false
	for _, source := range report.Sources {
		for _, model := range source.Models {
			if model == "from-proxy" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("未收到代理返回的模型列表: %#v", report.Sources)
	}
}
