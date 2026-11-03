// Package version 保存整个工具唯一的版本号来源。
//
// 作者 long: 之前版本号散落在 scripts/package-macos.sh 里，靠手工同步，
// 出现过 app 包和 CLI 报的版本不一致。打包脚本现在从这个常量读取。
package version

// Version 是当前发布版本，打包与 --version 都读它。
const Version = "1.7.0"
