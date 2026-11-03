# API Checker

检测 OpenAI / Anthropic 兼容接口是否可用，以及它们实际有多快。

填入 `Base URL` 与 `API Key`，拉取模型列表，再挑模型发一次真实对话请求，直接看到 HTTP 状态、首字耗时、总耗时与 token 用量。工具会主动模拟真实 coding agent 的流量（系统提示词 + 配套客户端请求头），因为很多网关正是按这些特征路由或限流的 —— 只用一句 `ping` 探测不出差异。

三种用法共用同一套检测内核：

- **桌面应用**：macOS 原生窗口，账号列表、模型表格、批量检测都在一个界面里。
- **浏览器预览**：`--serve` 起一个本地 HTTP 服务，浏览器打开即用。
- **CLI**：脚本与 CI 友好，支持 `--format json` 与 `--strict` 退出码。

## 快速开始

### 桌面应用

```bash
./scripts/build-desktop.sh   # 构建 → ./apicheck-desktop
./scripts/package-macos.sh   # 打包 → build/macos/API Checker.app / .dmg / .zip
```

把 `build/macos/API Checker.app` 拖进 `/Applications` 即可。DMG 里带 `Applications` 快捷入口，适合分发给其他 Mac。

### 浏览器预览

```bash
go run ./cmd/apicheck --serve --listen 127.0.0.1:8787
```

打开 `http://127.0.0.1:8787`。页面调用同进程的 `/api/models` 与 `/api/check`；桌面端则走 Wails 绑定的 Go 方法，两边行为一致。

### CLI

```bash
go run ./cmd/apicheck \
  --base-url "https://example.com/v1" \
  --api-key "sk-xxx" \
  --protocol auto \
  --mode both
```

API Key 也可来自环境变量：`APICHECK_API_KEY="sk-xxx" go run ./cmd/apicheck --base-url ...`

## 功能特性

**检测**

- OpenAI 风格 `GET /models`、`POST /chat/completions`、`POST /responses`；Anthropic 风格 `GET /models`、`POST /messages`。
- 非流式与 SSE 流式两条链路都能测，流式拆出建连 / 首包 / 空闲 / 最长总时长四个超时，长输出不会被普通请求总超时提前切断。
- 检测协议可多选（Chat / Responses / Claude），一次把同一个模型的几种形态都跑一遍。
- 批量检测逐条回传：每测完一个模型，表格立刻出现该行，检测弹窗实时显示「已测 N/M」，不用等整批跑完。
- 模型等级（推理强度）可选 `minimal` / `low` / `medium` / `high`，按协议映射到 `reasoning_effort`、`reasoning.effort`、`output_config.effort`。

**排查**

- 失败原因直接显示中文标签（限流、鉴权失败、端点不存在…），悬停可看原始 `error_class` 与错误详情。
- 完整保留返回正文与原始返回，方便对照兼容网关的实现差异。
- 请求预览同时给出 `curl` 与 POST 原始请求，可粘贴到终端复现。
- 流式响应即使 HTTP 200，只要出现 `response.failed` / `response.incomplete` / `error` 事件也判为失败并给出原因，不会把整段 SSE 原文当成成功。

**管理与留痕**

- 多账号：左侧账号列表保存多组 `Base URL` + `API Key`，可切换、勾选、改名、复制、分享。
- `获取当前` / `获取所有` 分别拉取当前账号或全部已勾选账号的模型，表格按账号分组、分组头可折叠。
- 模型表格支持实时检索；`--filter` / 搜索框同时作为后端拉取时的过滤条件。
- 检测结果缓存在本地，重开窗口即可看到上次结论；可一键导出 CSV / JSON。
- API Key 默认只显示脱敏尾号，点「显示」才切换明文。

## 检测能力

模型列表：

- `GET /models`

对话：

| 协议 | 端点 | 鉴权 |
| --- | --- | --- |
| OpenAI Chat | `POST /chat/completions` | `Authorization: Bearer <apiKey>` |
| OpenAI Responses | `POST /responses` | `Authorization: Bearer <apiKey>` |
| Anthropic Messages | `POST /messages` | `x-api-key: <apiKey>` + `anthropic-version: 2023-06-01` |

## CLI 参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--base-url` | — | API 根地址，例如 `https://example.com/v1` |
| `--api-key` | — | API Key，也可用 `APICHECK_API_KEY` |
| `--protocol` | `auto` | `auto` / `openai` / `openai-chat` / `openai-responses` / `anthropic` / `anthropic-messages` / `all` |
| `--mode` | `both` | `both` / `nonstream` / `stream` |
| `--prompt` | 内置短提示词 | 检测用的用户消息 |
| `--system-prompt` | `none` | 系统提示词预设，见下节 |
| `--reasoning-effort` | 空 | 模型等级：`minimal` / `low` / `medium` / `high` |
| `--max-tokens` | `256` | 输出上限（Anthropic 协议即 `max_tokens`） |
| `--concurrency` | `2` | 模型探测并发 |
| `--adaptive-concurrency` | 关 | 撞限流时把并发减半，之后缓慢加回 |
| `--retries` | `0` | 限流 / 5xx / 网络错误的重试次数 |
| `--filter` | 空 | 只测名字包含该文本的模型 |
| `--max-models` | `0` | 每个来源最多探测多少模型，`0` 不限 |
| `--timeout` | `60s` | 模型列表与非流式请求总超时 |
| `--connect-timeout` | `30s` | TCP/TLS 建连超时 |
| `--stream-first-timeout` | `60s` | 流式等待响应头 / 首个 SSE 包的超时 |
| `--stream-idle-timeout` | `60s` | 流式连续无新内容的空闲超时 |
| `--stream-max-duration` | `10m` | 单次流式请求最长总时长，`0` 不限 |
| `--proxy` | 空 | 出口代理，支持 `http` / `https` / `socks5` |
| `--header` | 空 | 额外请求头，每行一条 `Name: Value` |
| `--insecure-skip-verify` | 关 | 跳过 TLS 校验，用于自签名网关 |
| `--openai-ua-surface` | `cli` | OpenAI 兼容请求的身份表面：`cli` / `desktop` |
| `--anthropic-ua-surface` | `cli` | Anthropic 兼容请求的身份表面：`cli` / `desktop` |
| `--strict` | 关 | 只要存在「已执行且失败」的探测就退出码 1 |
| `--format` | `table` | `table` / `json` |
| `--output` | 空 | 把输出写入文件 |
| `--version` | — | 打印版本号后退出 |
| `--serve` / `--listen` / `--static-dir` | — | 启动本地 Web UI |

## 系统提示词预设

很多网关按 system prompt 特征路由或限流，短探测消息覆盖不到这些差异，所以工具内置了几份真实 agent 的系统提示词：

| 预设 | 来源 | 是否附带配套客户端请求头 |
| --- | --- | --- |
| `none` | 不发送 system prompt（默认） | — |
| `pi` | pi coding agent | 是 |
| `codex` | OpenAI Codex CLI 基础指令 | 是 |
| `codex-gpt-5` | Codex CLI 针对 GPT-5 系 | 否 |
| `opencode` | sst/opencode | 是 |
| `deepseek` | DeepSeek Harness（ACP） | 否 |
| `deepseek-web` | DeepSeek Web | 否 |

带配套请求头的预设会一并覆盖 `User-Agent`、`originator`、`x-stainless-*` 等字段，版本号从各家源码逐一对过，选择预设即整套模拟该 agent 的流量。

```bash
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --system-prompt codex
```

Web UI 与桌面端在「系统提示词预设」右侧有「预览」按钮：左边是每个协议实际发送的请求头，右边是完整系统提示词正文。

## 输出上限与截断

探测请求会带 `max_output_tokens`（Anthropic 协议为 `max_tokens`），默认 256。取值太小会把正常回答截断：网关返回 `response.incomplete` 或 `stop_reason=max_tokens`，工具识别为「截断」而不是失败（CLI 显示 `ok(截断)`），但内容只剩半句。

配合长系统提示词（例如 `codex-gpt-5` 约 20KB）与长提示词时建议调大：

```bash
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --max-tokens 1024
```

## 网络与请求头

```bash
# 出口代理，支持 http / https / socks5
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --proxy http://127.0.0.1:8080

# 自定义请求头，每行一条 Name: Value，# 开头为注释
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --header "x-gateway-token: abc123"
```

自定义头在 Agent 身份头之后写入，因此可以覆盖预设的 `User-Agent`；它们也会出现在请求预览里，方便核对网关实际收到什么。

## 限流与重试

`--retries` 对限流（429）、5xx 与网络类错误重试，退避 1s 起、上限 8s；认证失败与请求格式错误不重试。上游若给出建议等待时长（`Retry-After` 响应头，或响应体的 `retryAfterSeconds` / `retry_after`），重试以它为准，上限 30s，不会在网关要求的窗口内继续撞限流。

`--adaptive-concurrency` 打开后，一旦某次探测撞上限流就把并发窗口减半，之后每完成一个任务缓慢加回。

超时相关参数见上面的 [CLI 参数](#cli-参数)表。探测失败时，表格行下方会补一行缩进的 `↳` 说明；多层嵌套的网关错误会被拍平成「上游错误码：403 → 10605 + 上游详情 JSON」，不再是一整坨转义 JSON。

## 多账号

左侧「账号」列表管理多组连接信息，每个账号是一组独立的 `Base URL` + `API Key`：

- **新增 / 复制 / 删除**：`+ 新增` 建一个空白账号，`复制账号` 以当前账号为模板建副本。删除用条目右侧的垃圾桶图标，连点两次确认，且至少保留一个账号。
- **切换**：点击条目即切换表单中的连接信息，双击直接聚焦名称输入框。
- **勾选**：决定该账号是否参与「获取所有」与「全部检测」，取消勾选的账号仍保留已获取的数据。
- **获取当前 / 获取所有**：前者只请求当前账号，后者并发请求全部已勾选账号。两者都会先清空右侧列表再渲染，避免新旧数据混在一起。
- **账号状态点**：灰点＝还没成功拿到过模型；绿点＝已拿到模型且检测无失败（只看模型列表就算绿，不必先跑检测）；红点＝获取失败 / 没返回任何模型 / 检测有失败；橙点闪烁＝正在获取。悬停显示具体状态。
- **全部检测**：对已勾选账号当前已加载的全部模型发起检测，结果写回各自归属的账号行。
- **分享连接**：条目右侧的分享图标，分享该账号的 `Base URL,API Key`（`Base URL` 为空时不可用）。

账号列表与当前账号存在 `localStorage`（键 `apicheck:accounts:v5`），从旧版本升级时会自动把原来的单组连接信息转成第一个账号。检测结果缓存在 `apicheck:results:v1`，只保存状态、耗时与 token 用量，不含原始响应体。

## JSON 输出与 CI

```bash
# JSON 报告
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --format json

# CI：只要存在已执行且失败的探测就退出码 1
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --strict
```

表格里的 TOKENS 列格式为 `输入/输出/缓存/总计`，数据来自响应的 `usage` 字段（OpenAI 与 Anthropic 的命名已归一），网关未返回时显示 `-`。Web UI 与桌面端同样展示。

版本号只在 `internal/version/version.go` 维护一处，打包脚本与 `--version` 都读它。

## 项目结构

```text
.
├── assets/app-icon.svg             # 应用图标源文件
├── cmd/
│   ├── apicheck/                   # CLI 与本地 HTTP 服务
│   │   ├── main.go                 # 入口与 flag 定义
│   │   ├── flags.go                # protocol/mode/surface 的 flag.Value 实现
│   │   ├── serve.go                # -serve 启动
│   │   ├── server.go               # HTTP 路由与 JSON 编解码
│   │   └── table.go                # 终端表格渲染
│   ├── apicheck-desktop/           # Wails 桌面应用
│   │   ├── main.go                 # wails.Run 与窗口参数
│   │   ├── app.go                  # 绑定给前端的 App 方法
│   │   ├── window_darwin.go        # macOS 圆角透明窗口
│   │   ├── window_other.go         # 其他平台空实现
│   │   ├── frameworks_darwin.go    # macOS 链接的 framework
│   │   └── frontend/               # 由 ui/ 同步而来（embed）
│   └── icongen/                    # 由 SVG 生成应用图标 PNG
├── docs/technical-design.md
├── internal/
│   ├── apireq/                     # Web / 桌面 / CLI 共用的请求结构与参数解析
│   ├── checker/                    # 核心检测逻辑
│   │   ├── checker.go              # 探测编排
│   │   ├── models.go               # 模型列表发现
│   │   ├── body.go                 # 请求体构造
│   │   ├── stream.go               # 流式读取
│   │   ├── output.go               # token 用量、文本抽取、错误分类
│   │   ├── retry.go                # 重试与自适应并发
│   │   ├── requests.go             # Agent 身份头与自定义头
│   │   ├── identity.go             # 各 Agent 的 User-Agent 事实来源
│   │   ├── endpoints.go            # Base URL 归一化
│   │   ├── preview.go              # curl / POST 预览
│   │   ├── prompts.go              # 系统提示词预设
│   │   └── prompts/*.md            # 预设正文
│   ├── render/                     # Markdown 清洗与二维码生成
│   └── version/                    # 唯一版本号来源
├── ui/index.html                   # Web 与桌面共用的 UI 源文件
└── scripts/
    ├── build-desktop.sh            # 同步前端并构建桌面二进制
    ├── package-macos.sh            # 打 .app / .dmg / .zip
    ├── sync-desktop-ui.sh          # 同步 ui/ 到 Wails embed 目录
    └── ui-smoke.mjs                # 前端冒烟测试
```

## 开发验证

```bash
go test ./...          # 单元测试
gofmt -l .             # 代码风格（输出为空即通过）
go vet ./...
staticcheck ./...
```

前端冒烟测试（headless 跑通「获取模型 → 全部检测 → 导出 CSV → 刷新恢复」等关键链路）：

```bash
npm install --no-save playwright-core
npx playwright install chromium
node scripts/ui-smoke.mjs
```

改完 UI 后执行 `./scripts/build-desktop.sh`，它会把 `ui/index.html` 同步到 `cmd/apicheck-desktop/frontend/index.html` 再编译。

CI 配置在 `.github/workflows/ci.yml`：`go` 任务跑 gofmt / vet / test / build / staticcheck，`desktop` 任务在 macOS runner 上验证 Wails 构建，`ui-smoke` 任务跑上面的冒烟脚本。

## 图标

`assets/app-icon.svg`：浅色圆角底、透明安全区，中心是蓝 / 青 / 橙三色连接节点与绿色校验标记。留白用于避免 Dock 中出现贴边、过大的方块感。打包 `.app` 时可由它导出 PNG 或 `.icns`。
