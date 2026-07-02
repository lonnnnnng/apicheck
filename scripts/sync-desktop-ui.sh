#!/usr/bin/env zsh
set -euo pipefail

root_dir="${0:A:h:h}"

# 作者 long: 桌面端使用 Go embed 打包静态资源，构建前同步原型，避免预览页和桌面 App UI 走散。
mkdir -p "${root_dir}/cmd/apicheck-desktop/frontend"
cp "${root_dir}/prototype/index.html" "${root_dir}/cmd/apicheck-desktop/frontend/index.html"

