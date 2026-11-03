# API Checker

API Checker 是一个用于检测 OpenAI / Anthropic 兼容接口的本地工具。输入 `Base URL` 和 `API Key` 后，可以先获取模型列表，再选择模型发起对话测试，观察模型是否可用、首字响应耗时和总耗时。支持一次管理多个账号，并能对多个账号批量获取模型、批量检测。

![API Checker 图标](assets/app-icon.svg)

## 功能特性

- 支持 OpenAI 风格 `GET /models`、`POST /chat/completions`、`POST /responses`。
- 支持 Anthropic 风格 `GET /models`、`POST /messages`。
- 支持非流式和 SSE 流式对话检测。
- 流式检测拆分建连、首包、空闲和最长总时长超时，避免长输出被普通请求总超时提前切断。
- 支持模型名称实时检索。
- 支持单模型测试，测试协议和模式在弹窗中选择。
- 支持自定义检测提示词，并缓存最近一次账号列表、当前账号与提示词。
- 支持多账号：左侧「账号」列表保存多组 `Base URL` + `API Key`，点击切换、勾选参与批量检测、双击改名称。
- 支持一键获取全部已勾选账号的模型，表格按账号分组（分组头可折叠），并支持一键「全部检测」。
- API Key 默认只显示脱敏尾号，点「显示」才切换明文，避免截屏或共享屏幕时泄露。
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
  --max-tokens 256 \
  --timeout 60s \
  --connect-timeout 30s \
  --stream-first-timeout 60s \
  --stream-idle-timeout 60s \
  --stream-max-duration 10m
```

系统提示词预设（在设置里选择，旁边「预览」可查看该预设实际发送的请求头与完整系统提示词）：

用 `--system-prompt` 模拟真实 coding agent 的系统提示词流量（很多网关会按 system prompt 特征路由或限流，仅用短探测消息探不出这些差异）。可选值：`none`（默认，不发送 system prompt）、`pi`、`codex`、`codex-gpt-5`、`opencode`、`deepseek`、`deepseek-web`。Web UI 与桌面端在“检测提示词”上方和检测弹窗里都可切换。

`pi`、`codex`、`opencode` 预设同时内置了从各家源码核对的配套客户端请求头（User-Agent、`originator`、`x-stainless-*` 等），选择预设即整套模拟该 agent 流量；`codex-gpt-5` 与 `deepseek` 系预设只有提示词、不改变请求头。主页面“系统提示词预设”右侧的「预览」按钮可以查看当前预设整套内容：左边是该预设每个协议实际发送的请求头，右边是完整系统提示词。

```bash
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --system-prompt codex
```

输出上限：

探测请求会带 `max_output_tokens`（Anthropic 协议是 `max_tokens`），默认 256。取值太小会把正常回答截断：网关会回 `response.incomplete` / `stop_reason=max_tokens`，工具识别为「截断」而非失败（CLI 显示 `ok(截断)`），但内容只剩半句。配合长系统提示词（如 `codex-gpt-5` 约 20KB）和长提示词时建议调大。

```bash
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --max-tokens 1024
```

桌面端与 Web UI 在设置弹窗的「运行参数」里可以直接改「输出上限」。

模型等级（推理强度）：

设置弹窗的「运行参数」里有「模型等级」，取值 `minimal` / `low` / `medium` / `high`，默认「不设置」即完全不发送该字段。不同协议的字段名不同：`openai-chat` 用 `reasoning_effort`，`openai-responses` 用 `reasoning.effort`（与 codex 一致），`anthropic-messages` 用 `output_config.effort`（Anthropic 不认 `minimal`，会降级成 `low`）。

```bash
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx" --reasoning-effort high
```

和 Agent 一样带等级时，推理模型会先思考再回答，首字耗时通常明显变长，占总耗时的比例也更高；关掉等级再测一次可以区分「网关慢」和「模型在思考」。

## 网络与请求头

出口代理（`--proxy`，支持 `http` / `https` / `socks5`）：

```bash
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx"   --proxy http://127.0.0.1:8080
```

自定义请求头（`--header`，每行一条 `Name: Value`，`#` 开头为注释）。这些头在 Agent 身份头之后写入，所以能覆盖预设的 `User-Agent` 等；也会出现在请求预览里，方便核对网关实际收到什么：

```bash
go run ./cmd/apicheck --base-url "https://example.com/v1" --api-key "sk-xxx"   --header "x-gateway-token: abc123"
```

自签名网关用 `--insecure-skip-verify` 跳过证书校验。

## 限流与模型数量

`--retries` 在限流（429）、5xx 和网络类错误上重试，退避 1s 起、上限 8s；认证失败与请求格式错误不重试。

`--adaptive-concurrency` 打开后，一旦某次探测撞上限流就把并发窗口减半，之后每完成一个任务缓慢加回，避免越试越限。

`--max-models` 限制每个来源最多探测多少个模型，`--filter` 只测名字包含指定文本的模型（Web UI 顶部搜索框在拉取模型列表时也会作为过滤条件传给后端）。

失败原因说明：探测失败时，表格行下方会补一行缩进的 `↳` 说明，输出可读的错误原因（多层嵌套的网关错误会被拍平成“上游错误码：403 → 10605 + 上游详情 JSON”）。流式请求即使 HTTP 200，只要响应里出现 `response.failed`、`response.incomplete` 或 `error` 事件，也会判定为失败并显示原因，而不是把整段 SSE 原文当成成功响应。

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

## 多账号

左侧「账号」列表管理多组连接信息，每个账号是一组独立的 `Base URL` + `API Key`：

- **新增 / 复制 / 删除**：`+ 新增` 添加空白账号，`复制账号` 以当前账号为模板建一个副本。删除用账号条目右侧的垃圾桶图标，需连点两次确认，且至少保留一个账号。
- **切换**：点击账号条目即切换下方表单中的连接信息；双击条目会直接聚焦名称输入框。
- **勾选**：勾选框决定该账号是否参与「获取所有」与「全部检测」，取消勾选的账号仍保留已获取的数据。
- **获取当前 / 获取所有**：`获取当前` 只请求当前账号的模型列表；`获取所有` 并发请求全部已勾选账号。两者都会先清空右侧列表再渲染本次结果，避免新旧数据混在一起。表格按账号分组显示，分组头展示账号名称、host 与「可用 / 失败」统计，点分组头前的箭头可折叠。
- **账号状态点**：灰点表示还没成功获取过模型，绿点表示已成功拿到模型、且检测没发现失败（只看模型列表就算绿，不必先跑检测），红点表示获取失败 / 没返回任何模型 / 检测有失败，橙点（闪烁）表示正在获取。鼠标停在圆点上会显示具体状态。
- **全部检测**：对已勾选账号当前已加载的全部模型发起检测，结果写回各自归属的账号行。
- **分享连接**：账号条目右侧的分享图标，分享该账号的 `Base URL,API Key`（`Base URL` 为空时不可用）。

账号列表与当前账号存在 `localStorage`（键 `apicheck:accounts:v5`）；从旧版本升级时，原来的单组 `Base URL` / `API Key` 会自动转成第一个账号。模型列表与检测结果不落盘，刷新后需重新获取。

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
