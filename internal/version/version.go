// Package version 提供通过构建参数注入的版本信息。
package version

import "strings"

// Version 由构建时通过
// -ldflags "-X github.com/BeyondXinXin/harnessbox/internal/version.Version=..."
// 注入；本地开发或未注入时保持默认值 "dev"。它同时用作 payload 的版本标记：
// 版本变化时运行环境会被重新释放。
var Version = "dev"

// Display 返回用于界面展示的版本号：已带 v 前缀则原样返回，否则补上 v。
func Display() string {
	if Version == "" || Version == "dev" {
		return "dev"
	}
	if strings.HasPrefix(Version, "v") {
		return Version
	}
	return "v" + Version
}
