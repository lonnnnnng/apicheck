// Command printver 把 internal/version.Version 打到标准输出，
// 供打包脚本读取，保证版本号只有一个来源。
package main

import (
	"fmt"

	"apicheck/internal/version"
)

func main() {
	fmt.Println(version.Version)
}
