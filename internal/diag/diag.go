// Package diag 负责收集启动失败现场的诊断信息（版本、环境、日志尾部），
// 并弹出带「复制诊断信息」按钮的失败对话框：小白用户点一下即可把诊断内容
// 写入剪贴板，直接粘贴发给卖家，免去找日志文件。
package diag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/BeyondXinXin/deepseek-harness-box/internal/version"
)

// tailLines 是诊断信息携带的日志最大行数。
const tailLines = 50

// Context 是收集诊断信息所需的现场上下文。
type Context struct {
	LogPath    string // 日志文件路径；空表示日志不可用
	Port       int    // 本次启动尝试使用的端口；0 表示尚未选定
	RuntimeDir string // 运行环境目录；空表示尚未释放
}

// Collect 生成一段可直接粘贴发给售后的诊断文本，包含 HarnessBox 版本、
// DSH/Node 版本、Windows 版本、端口、运行环境文件状态与最近日志。所有
// 文本先经脱敏处理，绝不包含 API Key。
func Collect(ctx Context, failure string) string {
	var b strings.Builder
	b.WriteString("DeepSeekHarnessBox 诊断信息（已自动脱敏）\n")
	b.WriteString("生成时间：" + time.Now().Format("2006-01-02 15:04:05") + "\n")
	b.WriteString("HarnessBox 版本：" + version.Display() + "\n")
	b.WriteString("DSH 版本：" + dshVersion(ctx.RuntimeDir) + "\n")
	b.WriteString("Windows 版本：" + windowsVersion() + "\n")
	if ctx.Port > 0 {
		b.WriteString(fmt.Sprintf("当前端口：%d\n", ctx.Port))
	} else {
		b.WriteString("当前端口：未确定\n")
	}
	b.WriteString("Node 是否存在：" + existsStatus(nodePath(ctx.RuntimeDir)) + "\n")
	b.WriteString("dsh 是否存在：" + existsStatus(dshPath(ctx.RuntimeDir)) + "\n")
	if failure = strings.TrimSpace(failure); failure != "" {
		b.WriteString("错误信息：" + Redact(failure) + "\n")
	}
	if lines := tailLog(ctx.LogPath); len(lines) > 0 {
		b.WriteString(fmt.Sprintf("最近日志（最多 %d 行）：\n%s", len(lines), Redact(strings.Join(lines, "\n"))))
	}
	return b.String()
}

func nodePath(runtimeDir string) string {
	if runtimeDir == "" {
		return ""
	}
	return filepath.Join(runtimeDir, "node", "node.exe")
}

func dshPath(runtimeDir string) string {
	if runtimeDir == "" {
		return ""
	}
	return filepath.Join(runtimeDir, "dsh", "lib", "bin.js")
}

// existsStatus 报告文件是否存在，路径一并给出便于售后定位。
func existsStatus(path string) string {
	if path == "" {
		return "未知（运行环境未释放）"
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return "存在（" + path + "）"
	}
	return "不存在（" + path + "）"
}

// dshVersion 返回运行环境中内置的 DSH 与 Node 版本。优先读打包时写入的
// payload.json，缺失时兜底读 dsh 包自己的 package.json。
func dshVersion(runtimeDir string) string {
	if runtimeDir == "" {
		return "未知（运行环境未释放）"
	}
	if data, err := os.ReadFile(filepath.Join(runtimeDir, "payload.json")); err == nil {
		var meta struct {
			Node string `json:"node"`
			Dsh  string `json:"dsh"`
		}
		if json.Unmarshal(data, &meta) == nil && meta.Dsh != "" {
			if meta.Node == "" {
				return meta.Dsh
			}
			return fmt.Sprintf("%s（Node %s）", meta.Dsh, meta.Node)
		}
	}
	if data, err := os.ReadFile(filepath.Join(runtimeDir, "dsh", "package.json")); err == nil {
		var pkg struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Version != "" {
			return pkg.Version
		}
	}
	return "未知"
}

// rtlOsVersionInfoExW 是 RtlGetVersion 使用的 OSVERSIONINFOEXW 结构。
type rtlOsVersionInfoExW struct {
	dwOSVersionInfoSize uint32
	dwMajorVersion      uint32
	dwMinorVersion      uint32
	dwBuildNumber       uint32
	dwPlatformId        uint32
	szCSDVersion        [128]uint16
	wServicePackMajor   uint16
	wServicePackMinor   uint16
	wSuiteMask          uint16
	wProductType        byte
	wReserved           byte
}

var procRtlGetVersion = syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion")

// windowsVersion 返回 Windows 商业名称与内部版本号（如「Windows 11（build
// 26100）」）。RtlGetVersion 不受兼容性清单影响，结果比 GetVersionEx 真实。
func windowsVersion() string {
	info := rtlOsVersionInfoExW{dwOSVersionInfoSize: uint32(unsafe.Sizeof(rtlOsVersionInfoExW{}))}
	if result, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&info))); result != 0 {
		return "未知"
	}
	switch {
	case info.dwMajorVersion == 10 && info.dwMinorVersion == 0 && info.dwBuildNumber >= 22000:
		return fmt.Sprintf("Windows 11（build %d）", info.dwBuildNumber)
	case info.dwMajorVersion == 10 && info.dwMinorVersion == 0:
		return fmt.Sprintf("Windows 10（build %d）", info.dwBuildNumber)
	default:
		return fmt.Sprintf("Windows %d.%d（build %d）", info.dwMajorVersion, info.dwMinorVersion, info.dwBuildNumber)
	}
}

// tailLog 读取日志文件的最后 tailLines 行；文件不存在或读取失败返回 nil。
func tailLog(path string) []string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	return lines
}

// 脱敏规则：覆盖日志与错误信息中可能出现的 API Key。宁可多脱敏，不可漏。
var (
	// JSON 形式："apiKey": "sk-..."、"secret": "..." 等。
	reJSONKey = regexp.MustCompile(`(?i)("[a-z0-9_\-]*(?:key|token|secret|password)[a-z0-9_\-]*"\s*:\s*)"[^"]*"`)
	// key=value / key: value 形式（含 DEEPSEEK_API_KEY=sk-... 之类）。
	reKVKey = regexp.MustCompile(`(?i)\b([a-z0-9_\-]*(?:api[_-]?key|apikey|access[_-]?key|secret|token|password)[a-z0-9_\-]*\s*[=:])\s*("[^"]*"|[^\s,;}]+)`)
	// 裸 sk- 前缀密钥（DeepSeek 等厂商格式）。
	reSkKey = regexp.MustCompile(`sk-[A-Za-z0-9_\-]{6,}`)
	// HTTP 认证头中的 Bearer token。
	reBearer = regexp.MustCompile(`(?i)\b(Bearer)\s+[A-Za-z0-9._\-]+`)
)

// Redact 把文本中的 API Key、token、密码等敏感值替换为 ***。
func Redact(text string) string {
	text = reJSONKey.ReplaceAllString(text, `${1}"***"`)
	text = reKVKey.ReplaceAllString(text, `${1}***`)
	text = reSkKey.ReplaceAllString(text, "sk-***")
	text = reBearer.ReplaceAllString(text, `${1} ***`)
	return text
}
