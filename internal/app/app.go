// Package app 负责 DeepSeekHarnessBox 的启动编排：单实例锁、选择端口、释放运行环境、
// 启动 dsh web、打开浏览器、托盘消息循环与退出清理。
package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BeyondXinXin/deepseek-harness-box/internal/browser"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/config"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/diag"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/launcher"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/portcheck"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/runlog"
	hbruntime "github.com/BeyondXinXin/deepseek-harness-box/internal/runtime"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/shortcut"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/tray"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/ui"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/version"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/winutil"
)

const (
	singleInstanceName = `Local\BeyondXinXin.DeepSeekHarnessBox.Instance`
	// shortcutName 是桌面快捷方式文件名。
	shortcutName = "DeepSeekHarnessBox.lnk"
)

// 启动失败对话框的按钮 ID。退出按钮用 IDCANCEL(2)，这样 Esc / 标题栏 X
// 关闭对话框也等同于选择退出。
const (
	dialogRepair = 100
	dialogCopy   = 101
	dialogExit   = 2
)

// Main 是 DeepSeekHarnessBox 的入口。
func Main() {
	runtime.LockOSThread()

	explicitPort, hasExplicitPort := parsePortArg(os.Args[1:])

	instance, alreadyRunning, err := winutil.AcquireSingleInstance(singleInstanceName)
	if err != nil {
		winutil.MessageBox(0, "DeepSeekHarnessBox", "创建单进程锁失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}
	if alreadyRunning {
		port := explicitPort
		if !hasExplicitPort {
			port = portcheck.DefaultPort
		}
		_ = browser.Open(config.URL(port))
		winutil.MessageBox(0, "DeepSeekHarnessBox", "DeepSeekHarnessBox 已在运行。", winutil.MBIconInformation)
		return
	}
	defer instance.Close()

	dataDir := config.Directory(executableDir())
	if err := config.Ensure(dataDir); err != nil {
		winutil.MessageBox(0, "DeepSeekHarnessBox", "创建运行目录失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}
	logger, err := runlog.Open(config.LogsDir(dataDir))
	if err != nil {
		winutil.MessageBox(0, "DeepSeekHarnessBox", "打开日志失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}
	defer logger.Close()
	logger.Printf("DeepSeekHarnessBox 启动（版本 %s）", version.Display())

	process, port, err := startLoop(dataDir, explicitPort, hasExplicitPort, logger)
	if err != nil {
		// 启动失败且用户选择退出；失败原因已在对话框中告知。
		logger.Printf("用户放弃启动")
		return
	}

	url := config.URL(port)

	// 诊断：确认 API Key 凭据在界面中可编辑（未被视为「由启动环境提供」）。
	launcher.LogCredentialState(url, logger)

	if err := browser.Open(url); err != nil {
		logger.Printf("打开浏览器失败: %v", err)
		winutil.MessageBox(0, "DeepSeekHarnessBox", "已启动，但打开浏览器失败：\r\n"+err.Error()+"\r\n\r\n请手动访问 "+url, winutil.MBIconWarning)
	}

	err = tray.Run(tray.Actions{
		OnOpen: func() { _ = browser.Open(url) },
	})
	if err != nil {
		// 托盘创建失败：退化为后台驻留，直到 dsh 自行退出或用户结束本进程。
		logger.Printf("创建托盘图标失败: %v", err)
		_ = process.Wait()
		logger.Printf("dsh 已退出")
		return
	}

	process.Stop()
	logger.Printf("DeepSeekHarnessBox 已退出")
}

// createDesktopShortcut 在用户桌面创建（或重建）指向 target 的快捷方式。
func createDesktopShortcut(target string) (string, error) {
	return shortcut.Create(shortcutName, target, "DeepSeekHarnessBox 本地 AI 运行环境")
}

// appVersionFile 是主程序副本旁的版本标记文件名（与运行环境标记一致）。
const appVersionFile = ".version"

// ensureAppCopy 确保数据目录的 app 子目录中存有与当前版本一致的主程序
// 副本，返回桌面快捷方式应指向的 EXE 路径，以及本次是否执行了复制。
// 复制失败不阻塞启动：退回当前 EXE 路径并记录日志。
func ensureAppCopy(dataDir string, logger *runlog.Logger) (string, bool) {
	current, err := os.Executable()
	if err != nil {
		logger.Printf("获取当前可执行文件路径失败: %v", err)
		return "", false
	}
	target := config.AppExePath(dataDir)
	if appCopyUpToDate(target, version.Version) {
		return target, false
	}
	if err := os.MkdirAll(config.AppDir(dataDir), 0755); err != nil {
		logger.Printf("创建主程序目录失败: %v", err)
		return current, false
	}
	logger.Printf("复制主程序副本到 %s（版本 %s）", target, version.Version)
	if err := copyExecutable(current, target); err != nil {
		logger.Printf("复制主程序副本失败: %v", err)
		return current, false
	}
	if err := os.WriteFile(filepath.Join(config.AppDir(dataDir), appVersionFile), []byte(version.Version), 0644); err != nil {
		logger.Printf("写入主程序版本标记失败: %v", err)
	}
	return target, true
}

// appCopyUpToDate 报告主程序副本是否存在且版本标记与当前版本一致。
func appCopyUpToDate(target, ver string) bool {
	if _, err := os.Stat(target); err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(target), appVersionFile))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == ver
}

// copyExecutable 以流式复制可执行文件到 dst：先写临时文件再替换目标，
// 避免复制中断留下损坏的半截文件。
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// Windows 下 os.Rename 不能覆盖已存在文件，先删除旧副本再改名。
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// startLoop 反复尝试启动：任一步失败都会弹出「启动失败」对话框，用户可选择
// 「一键修复并重试」——只删除 runtime、保留 dsh-home 用户数据，重新释放内置
// 环境并重新清理端口后重试——「复制诊断信息」——把诊断内容写入剪贴板，
// 直接发给卖家排查——或「退出」。返回已就绪的 dsh 进程与端口；
// 用户放弃时返回错误。
func startLoop(dataDir string, explicitPort int, hasExplicitPort bool, logger *runlog.Logger) (*launcher.Process, int, error) {
	for {
		process, port, err := startOnce(dataDir, explicitPort, hasExplicitPort, logger)
		if err == nil {
			return process, port, nil
		}
		logger.Printf("启动失败: %v", err)

		// 面向小白的失败窗口：不直接抛 err.Error()，正文只保留动作说明；
		// 诊断信息（已脱敏，不含 API Key）折叠在「查看错误详情」里，
		// 「复制诊断信息」写入剪贴板的内容与之相同。
		report := diag.Collect(
			diag.Context{LogPath: logger.Path, Port: port, RuntimeDir: config.RuntimeDir(dataDir)},
			err.Error(),
		)
		choice := winutil.TaskDialog(
			"DeepSeekHarnessBox",
			"启动失败",
			"DeepSeekHarnessBox 没能正常启动。\r\n\r\n点击「一键修复并重试」，会自动清理并重建运行环境，然后重新启动；你的数据和设置都不会丢。\r\n\r\n点击「复制诊断信息」，把内容粘贴发给卖家即可快速排查问题。",
			[]winutil.TaskDialogButton{
				{ID: dialogRepair, Label: "一键修复并重试"},
				{ID: dialogCopy, Label: "复制诊断信息"},
				{ID: dialogExit, Label: "退出"},
			},
			0,
			winutil.TDErrorIcon,
			report,
		)
		switch choice {
		case dialogCopy:
			if copyErr := winutil.SetClipboardText(report); copyErr != nil {
				winutil.MessageBox(0, "DeepSeekHarnessBox", "复制诊断信息失败：\r\n"+copyErr.Error(), winutil.MBIconError)
			} else {
				winutil.MessageBox(0, "DeepSeekHarnessBox", "诊断信息已复制到剪贴板，直接粘贴发给卖家即可。", winutil.MBIconInformation)
			}
			continue // 重新弹窗，让用户继续选择修复或退出
		case dialogRepair:
			// 走下方一键修复流程
		default:
			return nil, 0, fmt.Errorf("用户选择退出")
		}

		logger.Printf("用户选择一键修复并重试")
		repairErr := ui.RunBusy(
			"正在修复运行环境，请稍候\n修复不会删除你的数据",
			func() error { return repairRuntime(dataDir, logger) },
		)
		if repairErr != nil {
			// 修复本身失败：回到循环，startOnce 会再次失败并重新弹窗。
			logger.Printf("一键修复失败: %v", repairErr)
		}
	}
}

// startOnce 执行一次完整启动：选端口 → 释放运行环境 → 启动 dsh → 等待就绪。
// 任一步失败返回错误，由 startLoop 决定是否修复重试。
func startOnce(dataDir string, explicitPort int, hasExplicitPort bool, logger *runlog.Logger) (*launcher.Process, int, error) {
	// 选择端口：显式 --port 优先；否则扫描 3080~3089，先清理 DSH/DeepSeekHarnessBox
	// 残留，再按 3081 → 3082..3089 → 3080 的偏好选择第一个空闲端口。
	var port int
	var err error
	if hasExplicitPort {
		port = explicitPort
		logger.Printf("使用指定端口 %d", port)
	} else {
		port, err = portcheck.FreePort(logger)
		if err != nil {
			logger.Printf("选择端口失败: %v", err)
			return nil, 0, fmt.Errorf("选择端口失败: %w", err)
		}
	}

	runtimeDir := config.RuntimeDir(dataDir)
	extract := func() error {
		dir, err := hbruntime.Extract(dataDir, version.Version, logger)
		if err == nil {
			runtimeDir = dir
		}
		return err
	}

	// 主程序常驻副本：首次运行（或版本更新）时把当前 EXE 复制到
	// %LOCALAPPDATA%\...\app\DeepSeekHarnessBox.exe，桌面快捷方式永远指向
	// 该副本，用户之后清理“下载”目录也不会让桌面图标失效。
	var appExe string
	appCopied := false
	firstExtract := hbruntime.NeedsExtract(runtimeDir, version.Version)
	if firstExtract {
		// 首次启动（或版本升级、修复后）需要解压运行环境，弹出「正在初始化」
		// 窗口避免用户误以为卡死；窗口期间一并完成主程序副本复制。
		logger.Printf("释放运行环境到 %s", runtimeDir)
		err = ui.RunBusy(
			"正在释放运行环境，请稍候\n首次启动或修复后需要重新释放内置环境，可能需要一点时间",
			func() error {
				appExe, appCopied = ensureAppCopy(dataDir, logger)
				return extract()
			},
		)
		if err != nil {
			logger.Printf("释放运行环境失败: %v", err)
			return nil, port, fmt.Errorf("释放运行环境失败: %w", err)
		}
	} else {
		appExe, appCopied = ensureAppCopy(dataDir, logger)
		if err = extract(); err != nil {
			logger.Printf("释放运行环境失败: %v", err)
			return nil, port, fmt.Errorf("释放运行环境失败: %w", err)
		}
	}

	// 首次运行/升级、副本刚被更新或桌面快捷方式缺失时，创建（或重建）
	// 快捷方式，目标固定为 app 目录下的常驻副本。
	if appExe != "" && (firstExtract || appCopied || !shortcut.Exists(shortcutName)) {
		if linkPath, linkErr := createDesktopShortcut(appExe); linkErr != nil {
			logger.Printf("创建桌面快捷方式失败: %v", linkErr)
		} else {
			logger.Printf("桌面快捷方式已创建: %s", linkPath)
		}
	}

	nodePath := filepath.Join(runtimeDir, "node", "node.exe")
	scriptPath := filepath.Join(runtimeDir, "dsh", "lib", "bin.js")
	dshHome := config.DshHomeDir(dataDir)

	process, err := launcher.Start(nodePath, scriptPath, dshHome, workspaceDir(), port, logger)
	if err != nil {
		return nil, port, err
	}

	url := config.URL(port)
	if err := launcher.WaitReady(url, 45*time.Second, logger); err != nil {
		logger.Printf("等待服务就绪失败: %v", err)
		process.Stop()
		return nil, port, err
	}
	return process, port, nil
}

// repairRuntime 执行一键修复：只删除 runtime 运行环境目录（保留 dsh-home
// 用户数据），随后重新释放内置环境。端口清理由下一次 startOnce 的
// FreePort 完成。
func repairRuntime(dataDir string, logger *runlog.Logger) error {
	runtimeDir := config.RuntimeDir(dataDir)
	logger.Printf("一键修复：删除运行环境 %s（保留用户数据）", runtimeDir)
	if err := os.RemoveAll(runtimeDir); err != nil {
		return fmt.Errorf("清理运行环境失败: %w", err)
	}
	dir, err := hbruntime.Extract(dataDir, version.Version, logger)
	if err != nil {
		return fmt.Errorf("重新释放运行环境失败: %w", err)
	}
	logger.Printf("一键修复完成：运行环境已重新释放到 %s", dir)
	return nil
}

// parsePortArg 解析 --port N 或 --port=N。返回 (端口, 是否显式指定)。
func parsePortArg(args []string) (int, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--port" || arg == "-port":
			if i+1 < len(args) {
				if value, err := strconv.Atoi(args[i+1]); err == nil && value > 0 && value < 65536 {
					return value, true
				}
			}
		case strings.HasPrefix(arg, "--port="):
			if value, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil && value > 0 && value < 65536 {
				return value, true
			}
		}
	}
	return 0, false
}

func executableDir() string {
	executable, err := os.Executable()
	if err != nil {
		workingDir, _ := os.Getwd()
		return workingDir
	}
	return filepath.Dir(executable)
}

func workspaceDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return executableDir()
}
