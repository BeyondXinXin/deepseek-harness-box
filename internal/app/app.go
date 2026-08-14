// Package app 负责 DeepSeekHarnessBox 的启动编排：单实例锁、选择端口、释放运行环境、
// 启动 dsh web、打开浏览器、托盘消息循环与退出清理。
package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BeyondXinXin/deepseek-harness-box/internal/browser"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/config"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/launcher"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/portcheck"
	hbruntime "github.com/BeyondXinXin/deepseek-harness-box/internal/runtime"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/runlog"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/shortcut"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/tray"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/ui"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/version"
	"github.com/BeyondXinXin/deepseek-harness-box/internal/winutil"
)

const singleInstanceName = `Local\BeyondXinXin.DeepSeekHarnessBox.Instance`

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

	// 选择端口：显式 --port 优先；否则扫描 3080~3089，先清理 DSH/DeepSeekHarnessBox
	// 残留，再按 3081 → 3082..3089 → 3080 的偏好选择第一个空闲端口。
	var port int
	if hasExplicitPort {
		port = explicitPort
		logger.Printf("使用指定端口 %d", port)
	} else {
		port, err = portcheck.FreePort(logger)
		if err != nil {
			logger.Printf("选择端口失败: %v", err)
			winutil.MessageBox(0, "DeepSeekHarnessBox", "启动失败：\r\n"+err.Error(), winutil.MBIconError)
			return
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
		// 首次启动（或版本升级）需要解压运行环境，弹出「正在初始化」窗口
		// 避免用户误以为卡死；释放成功后顺手在桌面创建快捷方式。
		logger.Printf("首次启动：释放运行环境到 %s", runtimeDir)
		err = ui.RunBusy(
			"正在初始化运行环境，请稍候\n首次启动需要将运行环境释放到本地，可能需要一点时间",
			extract,
		)
		if err != nil {
			logger.Printf("释放运行环境失败: %v", err)
			winutil.MessageBox(0, "DeepSeekHarnessBox", "释放运行环境失败：\r\n"+err.Error(), winutil.MBIconError)
			return
		}
		if linkPath, linkErr := createDesktopShortcut(); linkErr != nil {
			logger.Printf("创建桌面快捷方式失败: %v", linkErr)
		} else {
			logger.Printf("桌面快捷方式已创建: %s", linkPath)
		}
	} else if err = extract(); err != nil {
		logger.Printf("释放运行环境失败: %v", err)
		winutil.MessageBox(0, "HarnessBox", "释放运行环境失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}

	nodePath := filepath.Join(runtimeDir, "node", "node.exe")
	scriptPath := filepath.Join(runtimeDir, "dsh", "lib", "bin.js")
	dshHome := config.DshHomeDir(dataDir)

	process, err := launcher.Start(nodePath, scriptPath, dshHome, workspaceDir(), port, logger)
	if err != nil {
		logger.Printf("启动失败: %v", err)
		winutil.MessageBox(0, "DeepSeekHarnessBox", "启动失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}

	url := config.URL(port)
	if err := launcher.WaitReady(url, 45*time.Second, logger); err != nil {
		logger.Printf("等待服务就绪失败: %v", err)
		process.Stop()
		winutil.MessageBox(0, "DeepSeekHarnessBox", "启动失败：\r\n"+err.Error()+"\r\n\r\n请查看日志：\r\n"+logger.Path, winutil.MBIconError)
		return
	}

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

// createDesktopShortcut 在用户桌面创建指向当前可执行文件的快捷方式。
func createDesktopShortcut() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return shortcut.Create("DeepSeekHarnessBox.lnk", executable, "DeepSeekHarnessBox 本地 AI 运行环境")
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
