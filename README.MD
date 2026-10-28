# API Checker

API Checker 是一个用于检测 OpenAI / Anthropic 兼容接口的本地工具。输入 `Base URL` 和 `API Key` 后，可以先获取模型列表，再选择模型发起对话测试，观察模型是否可用、首字响应耗时和总耗时。

![API Checker 图标](assets/app-icon.svg)

## 功能特性

- 支持 OpenAI 风格 `GET /models`、`POST /chat/completions`、`POST /responses`。
- 支持 Anthropic 风格 `GET /models`、`POST /messages`。
- 支持非流式和 SSE 流式对话检测。
- 流式检测拆分建连、首包、空闲和最长总时长超时，避免长输出被普通请求总超时提前切断。
- 支持模型名称实时检索。
- 支持单模型测试，测试协议和模式在弹窗中选择。
- 支持自定义检测提示词，并缓存最近一次 `Base URL`、`API Key` 和提示词。
- 支持请求预览，包含 `curl` 和 POST 原始请求。
- 支持查看返回正文和完整原始返回，便于排查兼容网关问题。
- 选择 agent 预设时自动附带该 agent 的真实客户端请求头（pi、codex、opencode），请求头与系统提示词配套切换。

## 图标设计

应用图标位于：

```text
assets/app-icon.svg
```

图标使用浅色圆角底和透明安全区，中心保留蓝色、青色和橙色的连接节点与绿色校验标记，表达“模型接口接入检测”的用途。透明留白用于避免 Dock 中出现贴边、过大的方块感。

如果后续要打包成 `.app`，可以基于 `assets/app-icon.svg` 导出 `appicon.png` 或 `.icns`，再接入 Wails 打包资源。

## 项目结构

```text
.
├── assets/                         # 设计资产
│   └── app-icon.svg                # 应用图标
├── cmd/
│   ├── apicheck/                   # CLI 和本地 HTTP 预览服务入口
│   └── apicheck-desktop/           # Wails 桌面应用入口
├── docs/
│   └── api-checker-technical-design.md
├── internal/
│   └── checker/                    # 核心检测逻辑
├── prototype/
│   └── index.html                  # 原型和桌面 UI 源文件
└── scripts/
    ├── build-desktop.sh            # 同步前端并构建桌面二进制
    └── sync-desktop-ui.sh          # 同步 prototype 到 Wails embed 目录
```

## 桌面应用

构建桌面二进制：

```bash
./scripts/build-desktop.sh
```

运行：

```bash
./apicheck-desktop
```

构建脚本会先把 `prototype/index.html` 同步到 `cmd/apicheck-desktop/frontend/index.html`，再使用 Wails production tags 编译桌面应用。

## 生成 macOS 安装包

生成 `.app`、`.dmg` 和 `.zip`：

```bash
./scripts/package-macos.sh
```

输出位置：

```text
build/macos/API Checker.app
build/macos/API Checker.dmg
build/macos/API Checker-macOS.zip
```

本机安装可以把 `build/macos/API Checker.app` 复制到 `/Applications`。DMG 里也包含 `Applications` 快捷入口，适合分发给其他 Mac 使用。

## CLI 使用

基础检测：

```bash
go run ./cmd/apicheck \
  --base-url "https://example.com/v1" \
  --api-key "sk-xxx" \
  --protocol auto \
  --mode both \
  --concurrency 2 \
  --timeout 60s \
  --connect-timeout 30s \
  --stream-first-timeout 60s \
  --stream-idle-timeout 60s \
  --stream-max-duration 10m
```

系统提示词预设：

用 `--system-prompt` 模拟真实 coding agent 的系统提示词流量（很多网关会按 system prompt 特征路由或限流，仅用短探测消息探不出这些差异）。可选值：`none`（默认，不发送 system prompt）、`pi`、`codex`、`codex-gpt-5`、`opencode`、`deepseek`、`deepseek-web`。Web UI 与桌面端在“检测提示词”上方和检测弹窗里都可切换。

```bash
`pi`、`codex`、`opencode` 预设同时内置了从各家源码核对的配套客户端请求头（User-Agent、`originator`、`x-stainless-*` 等），选择预设即整套模拟该 agent 流量；`codex-gpt-5` 与 `deepseek` 系预设只有提示词、不改变请求头。设置面板可查看当前预设每个协议实际发送的请求头。

go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --system-prompt codex
```

超时说明：

- `--timeout`：模型列表和非流式请求总超时，默认 `60s`。
- `--connect-timeout`：TCP/TLS 建连超时，默认 `30s`。
- `--stream-first-timeout`：流式请求等待响应头/首个 SSE 包的超时，默认 `60s`。
- `--stream-idle-timeout`：流式过程中连续没有新内容的空闲超时，默认 `60s`。
- `--stream-max-duration`：单次流式请求最长总时长，默认 `10m`；填 `0` 表示不限制。

也可以通过环境变量传入 API Key：

```bash
APICHECK_API_KEY="sk-xxx" go run ./cmd/apicheck --base-url "https://example.com/v1"
```

表格里的 TOKENS 列格式为 `输入/输出/缓存/总计`（数据来自响应的 `usage` 字段，OpenAI 与 Anthropic 的命名已归一；网关未返回 usage 时显示 `-`）。Web UI 的模型表格与检测弹窗同样展示。

输出 JSON：

```bash
go run ./cmd/apicheck \
  --base-url "https://example.com/v1" \
  --api-key "sk-xxx" \
  --format json
```

CI / 脚本中可用 `--strict`：只要存在“已执行且失败”的探测就退出码 1，全部通过退出码 0：

```bash
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --strict
```

## 浏览器预览

启动本地 HTTP 服务：

```bash
go run ./cmd/apicheck --serve --listen 127.0.0.1:8787
```

打开：

```text
http://127.0.0.1:8787
```

浏览器预览会调用同进程的 `/api/models` 和 `/api/check`。桌面应用中则优先调用 Wails 绑定的 Go 方法。

## 支持的接口

模型列表：

- `GET /models`

OpenAI 兼容对话：

- `POST /chat/completions`
- `POST /responses`

Anthropic 兼容对话：

- `POST /messages`

鉴权方式：

- OpenAI 风格：`Authorization: Bearer <apiKey>`
- Anthropic 风格：`x-api-key: <apiKey>` + `anthropic-version: 2023-06-01`

## 开发验证

运行测试：

```bash
go test ./...
```

检查桌面构建：

```bash
./scripts/build-desktop.sh
```

如果只修改 UI，请改 `prototype/index.html`，再执行构建脚本同步到桌面端。
