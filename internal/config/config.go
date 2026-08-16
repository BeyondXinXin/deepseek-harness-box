// Package config 提供 DeepSeekHarnessBox 的路径约定与本地服务配置。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// AppVendor 是厂商目录名，数据根目录最终落在
	// %LOCALAPPDATA%\BeyondXinXin\DeepSeekHarnessBox。
	AppVendor = "BeyondXinXin"
	// AppName 是应用数据目录名。
	AppName = "DeepSeekHarnessBox"
	// AppExeName 是主程序常驻副本的文件名，首次运行会把当前 EXE 复制为
	// 该副本，桌面快捷方式始终指向这里。
	AppExeName = "DeepSeekHarnessBox.exe"
	// DefaultHost 是 dsh web 的监听地址。
	DefaultHost = "127.0.0.1"
)

// Directory 返回 DeepSeekHarnessBox 的数据根目录，默认
// %LOCALAPPDATA%\BeyondXinXin\DeepSeekHarnessBox；LOCALAPPDATA 缺失时回退到
// fallbackDir\BeyondXinXin\DeepSeekHarnessBox。
func Directory(fallbackDir string) string {
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return filepath.Join(local, AppVendor, AppName)
	}
	return filepath.Join(fallbackDir, AppVendor, AppName)
}

// Ensure 创建数据根目录与日志目录。
func Ensure(base string) error {
	if err := os.MkdirAll(base, 0755); err != nil {
		return err
	}
	return os.MkdirAll(LogsDir(base), 0755)
}

// RuntimeDir 返回运行环境释放目录（payload 解压后所在位置）。
func RuntimeDir(base string) string {
	return filepath.Join(base, "runtime")
}

// AppDir 返回主程序常驻副本目录。首次运行（或版本更新）时会把当前 EXE
// 复制到这里，桌面快捷方式固定指向该副本，避免用户清理“下载”目录后
// 快捷方式失效。
func AppDir(base string) string {
	return filepath.Join(base, "app")
}

// AppExePath 返回主程序常驻副本的完整路径。
func AppExePath(base string) string {
	return filepath.Join(AppDir(base), AppExeName)
}

// DshHomeDir 返回 DSH 用户数据目录（作为 DSH_HOME 传入子进程）。
func DshHomeDir(base string) string {
	return filepath.Join(base, "dsh-home")
}

// LogsDir 返回日志目录。
func LogsDir(base string) string {
	return filepath.Join(base, "logs")
}

// URL 返回 dsh web 的本地访问地址。
func URL(port int) string {
	return fmt.Sprintf("http://%s:%d", DefaultHost, port)
}
