// Package app 负责 DeepSeekHarnessBox 的启动编排：单实例锁、选择端口、释放运行环境、
// 启动 dsh web、打开浏览器、托盘消息循环与退出清理。
package app

import (
	"fmt"
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
	"github.com/BeyondXinXin/deepseek-harness-box/internal/tray"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/ui"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/version"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/winutil"
)

const singleInstanceName = `Local\BeyondXinXin.DeepSeekHarnessBox.Instance`

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

	if hbruntime.NeedsExtract(runtimeDir, version.Version) {
		// 首次启动（或版本升级、修复后）需要解压运行环境，弹出「正在初始化」
		// 窗口避免用户误以为卡死。
		logger.Printf("释放运行环境到 %s", runtimeDir)
		err = ui.RunBusy(
			"正在释放运行环境，请稍候\n首次启动或修复后需要重新释放内置环境，可能需要一点时间",
			extract,
		)
		if err != nil {
			logger.Printf("释放运行环境失败: %v", err)
			return nil, port, fmt.Errorf("释放运行环境失败: %w", err)
		}
	} else if err = extract(); err != nil {
		logger.Printf("释放运行环境失败: %v", err)
		return nil, port, fmt.Errorf("释放运行环境失败: %w", err)
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
