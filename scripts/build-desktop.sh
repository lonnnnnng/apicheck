#!/usr/bin/env zsh
set -euo pipefail

root_dir="${0:A:h:h}"

"${root_dir}/scripts/sync-desktop-ui.sh"

# 作者 long: Wails production 运行时必须带 desktop,production tags；普通 go build 会编译但启动时拒绝运行。
go build -tags desktop,production -o "${root_dir}/apicheck-desktop" "${root_dir}/cmd/apicheck-desktop"
