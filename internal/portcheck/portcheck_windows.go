// Package portcheck 负责端口占用探测、进程识别与清理。
package portcheck

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/BeyondXinXin/harnessbox/internal/runlog"
	"github.com/BeyondXinXin/harnessbox/internal/winutil"
)

const (
	// Host 是 dsh web 的监听地址。
	Host = "127.0.0.1"
	// PortStart 与 PortEnd 构成自动选端口的扫描区间。
	PortStart = 3080
	PortEnd   = 3089
	// DefaultPort 是首选端口。
	DefaultPort = 3081
)

// Info 描述一个端口的占用情况。
type Info struct {
	Port        int
	Occupied    bool
	PID         int
	ProcessName string
}

// PreferredPorts 返回按偏好排序的候选端口：3081 → 3082..3089 → 3080。
func PreferredPorts() []int {
	ports := make([]int, 0, PortEnd-PortStart+1)
	ports = append(ports, DefaultPort)
	for p := DefaultPort + 1; p <= PortEnd; p++ {
		ports = append(ports, p)
	}
	for p := PortStart; p < DefaultPort; p++ {
		ports = append(ports, p)
	}
	return ports
}

// Inspect 探测 host:port 是否被占用；被占用时返回占用进程的 PID 与进程名。
func Inspect(host string, port int) (Info, error) {
	// 必须探测具体地址：Windows 下 0.0.0.0 通配绑定可与已占用的具体地址共存。
	listener, err := net.Listen("tcp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		_ = listener.Close()
		return Info{Port: port}, nil
	}
	pid, lookupErr := listeningPID(port)
	if lookupErr != nil {
		return Info{Port: port, Occupied: true}, nil
	}
	return Info{Port: port, Occupied: true, PID: pid, ProcessName: processName(pid)}, nil
}

// WaitUntilFree 轮询直到端口空闲或超时。
func WaitUntilFree(host string, port int, timeout time.Duration) (Info, error) {
	deadline := time.Now().Add(timeout)
	for {
		info, err := Inspect(host, port)
		if err != nil {
			return info, err
		}
		if !info.Occupied {
			return info, nil
		}
		if time.Now().After(deadline) {
			return info, fmt.Errorf("端口 %d 仍被占用", port)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// Terminate 结束占用进程及其子进程树。
func Terminate(info Info) error {
	if !info.Occupied || info.PID == 0 {
		return errors.New("无法确定端口占用进程")
	}
	if info.PID == os.Getpid() || info.PID == 4 {
		return fmt.Errorf("拒绝终止受保护进程 PID %d", info.PID)
	}
	cmd := exec.Command("taskkill.exe", "/PID", strconv.Itoa(info.PID), "/T", "/F")
	winutil.HideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("关闭进程失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// IsDSHOrHarnessBox 判断占用进程是否为 DSH（node.exe 运行 dsh 的 bin.js）
// 或 HarnessBox 自身。
func IsDSHOrHarnessBox(pid int) bool {
	name := processName(pid)
	if strings.EqualFold(name, "HarnessBox.exe") {
		return true
	}
	if !strings.EqualFold(name, "node.exe") {
		return false
	}
	commandLine := processCommandLine(pid)
	return strings.Contains(strings.ToLower(commandLine), "bin.js")
}

// FreePort 先清理 3080~3089 上残留的 DSH/HarnessBox 进程，再按偏好顺序返回
// 第一个空闲端口。
func FreePort(logger *runlog.Logger) (int, error) {
	// 第一阶段：清理本区间内被 DSH / HarnessBox 占用的端口。
	for p := PortStart; p <= PortEnd; p++ {
		info, err := Inspect(Host, p)
		if err != nil {
			logger.Printf("探测端口 %d 失败: %v", p, err)
			continue
		}
		if !info.Occupied || !IsDSHOrHarnessBox(info.PID) {
			continue
		}
		logger.Printf("端口 %d 被 DSH/HarnessBox 占用（PID %d, %s），将其结束", p, info.PID, info.ProcessName)
		if err := Terminate(info); err != nil {
			logger.Printf("结束进程失败: %v", err)
			continue
		}
		if _, err := WaitUntilFree(Host, p, 6*time.Second); err != nil {
			logger.Printf("端口 %d 未释放: %v", p, err)
		}
	}

	// 第二阶段：选择第一个空闲端口。
	for _, p := range PreferredPorts() {
		info, err := Inspect(Host, p)
		if err != nil {
			logger.Printf("探测端口 %d 失败: %v", p, err)
			continue
		}
		if !info.Occupied {
			logger.Printf("选择端口 %d", p)
			return p, nil
		}
		logger.Printf("端口 %d 被 %s（PID %d）占用，跳过", p, info.ProcessName, info.PID)
	}
	return 0, fmt.Errorf("端口 %d~%d 均被占用", PortStart, PortEnd)
}

func listeningPID(port int) (int, error) {
	cmd := exec.Command("netstat.exe", "-ano", "-p", "tcp")
	winutil.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return parseNetstatPID(string(output), port)
}

func parseNetstatPID(output string, port int) (int, error) {
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		localAddress := fields[1]
		state := fields[len(fields)-2]
		if !strings.HasSuffix(localAddress, suffix) || !strings.EqualFold(state, "LISTENING") {
			continue
		}
		pid, err := strconv.Atoi(fields[len(fields)-1])
		if err == nil {
			return pid, nil
		}
	}
	return 0, errors.New("未找到监听进程")
}

func processName(pid int) string {
	if pid == 0 {
		return ""
	}
	cmd := exec.Command("tasklist.exe", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	winutil.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	records, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil || len(records) == 0 || len(records[0]) == 0 {
		return ""
	}
	return strings.TrimSpace(records[0][0])
}

func processCommandLine(pid int) string {
	if pid == 0 {
		return ""
	}
	script := fmt.Sprintf("(Get-CimInstance Win32_Process -Filter 'ProcessId=%d').CommandLine", pid)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	winutil.HideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
