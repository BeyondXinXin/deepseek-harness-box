// Package app 负责 HarnessBox 的启动编排：单实例锁、选择端口、释放运行环境、
// 启动 dsh web、打开浏览器、托盘消息循环与退出清理。
package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BeyondXinXin/harnessbox/internal/browser"
	"github.com/BeyondXinXin/harnessbox/internal/config"
	"github.com/BeyondXinXin/harnessbox/internal/launcher"
	"github.com/BeyondXinXin/harnessbox/internal/portcheck"
	hbruntime "github.com/BeyondXinXin/harnessbox/internal/runtime"
	"github.com/BeyondXinXin/harnessbox/internal/runlog"
	"github.com/BeyondXinXin/harnessbox/internal/tray"
	"github.com/BeyondXinXin/harnessbox/internal/version"
	"github.com/BeyondXinXin/harnessbox/internal/winutil"
)

const singleInstanceName = `Local\BeyondXinXin.HarnessBox.Instance`

// Main 是 HarnessBox 的入口。
func Main() {
	runtime.LockOSThread()

	explicitPort, hasExplicitPort := parsePortArg(os.Args[1:])

	instance, alreadyRunning, err := winutil.AcquireSingleInstance(singleInstanceName)
	if err != nil {
		winutil.MessageBox(0, "HarnessBox", "创建单进程锁失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}
	if alreadyRunning {
		port := explicitPort
		if !hasExplicitPort {
			port = portcheck.DefaultPort
		}
		_ = browser.Open(config.URL(port))
		winutil.MessageBox(0, "HarnessBox", "HarnessBox 已在运行。", winutil.MBIconInformation)
		return
	}
	defer instance.Close()

	dataDir := config.Directory(executableDir())
	if err := config.Ensure(dataDir); err != nil {
		winutil.MessageBox(0, "HarnessBox", "创建运行目录失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}
	logger, err := runlog.Open(config.LogsDir(dataDir))
	if err != nil {
		winutil.MessageBox(0, "HarnessBox", "打开日志失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}
	defer logger.Close()
	logger.Printf("HarnessBox 启动（版本 %s）", version.Display())

	// 选择端口：显式 --port 优先；否则扫描 3080~3089，先清理 DSH/HarnessBox
	// 残留，再按 3081 → 3082..3089 → 3080 的偏好选择第一个空闲端口。
	var port int
	if hasExplicitPort {
		port = explicitPort
		logger.Printf("使用指定端口 %d", port)
	} else {
		port, err = portcheck.FreePort(logger)
		if err != nil {
			logger.Printf("选择端口失败: %v", err)
			winutil.MessageBox(0, "HarnessBox", "启动失败：\r\n"+err.Error(), winutil.MBIconError)
			return
		}
	}

	runtimeDir, err := hbruntime.Extract(dataDir, version.Version, logger)
	if err != nil {
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
		winutil.MessageBox(0, "HarnessBox", "启动失败：\r\n"+err.Error(), winutil.MBIconError)
		return
	}

	url := config.URL(port)
	if err := launcher.WaitReady(url, 45*time.Second, logger); err != nil {
		logger.Printf("等待服务就绪失败: %v", err)
		process.Stop()
		winutil.MessageBox(0, "HarnessBox", "启动失败：\r\n"+err.Error()+"\r\n\r\n请查看日志：\r\n"+logger.Path, winutil.MBIconError)
		return
	}

	if err := browser.Open(url); err != nil {
		logger.Printf("打开浏览器失败: %v", err)
		winutil.MessageBox(0, "HarnessBox", "已启动，但打开浏览器失败：\r\n"+err.Error()+"\r\n\r\n请手动访问 "+url, winutil.MBIconWarning)
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
	logger.Printf("HarnessBox 已退出")
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
