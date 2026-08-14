// Package launcher 负责启动、等待并结束内置的 node.exe + dsh web 子进程。
package launcher

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/BeyondXinXin/harnessbox/internal/runlog"
)

const (
	jobObjectExtendedLimitInfoClass = 9
	jobObjectLimitKillOnJobClose    = 0x2000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// Process 封装已启动的 dsh 子进程与其所属的 Job 对象。
type Process struct {
	cmd      *exec.Cmd
	job      syscall.Handle
	logger   *runlog.Logger
	stopOnce sync.Once
	waitOnce sync.Once
	waitErr  error
}

// inheritedCredentialEnv 是从父进程继承环境中剔除的 API Key 变量。dsh 把
// 继承环境中的 API Key 视为「由启动环境提供（只读）」，会锁死 Web 界面
// 「设置 → 模型」的密钥输入框；剔除后用户即可在界面中直接输入自己的 Key。
var inheritedCredentialEnv = map[string]struct{}{
	"DEEPSEEK_API_KEY": {},
}

// filterInheritedEnv 返回剔除 inheritedCredentialEnv 之后的继承环境列表。
// Windows 环境变量名不区分大小写，比较前统一转大写。
func filterInheritedEnv(environ []string) []string {
	filtered := environ[:0]
	for _, entry := range environ {
		name := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			name = entry[:index]
		}
		if _, blocked := inheritedCredentialEnv[strings.ToUpper(name)]; blocked {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// Start 启动 node.exe <script> web --port <port>，并把它挂到 kill-on-close 的
// Job 对象上，保证 HarnessBox 退出（含被强杀）时整棵进程树一起结束。
func Start(nodePath, scriptPath, dshHome, cwd string, port int, logger *runlog.Logger) (*Process, error) {
	cmd := exec.Command(nodePath, scriptPath, "web", "--port", strconv.Itoa(port))
	cmd.Dir = cwd
	// DSH_HOME 指向 HarnessBox 自己的数据目录；原生插件缓存也重定向到
	// 同一数据目录下，避免第三方插件往 LOCALAPPDATA 根目录写缓存。
	// 继承环境中剔除 API Key 变量，避免界面把它锁成「由启动环境提供（只读）」。
	nativeCacheDir := filepath.Join(filepath.Dir(dshHome), "native-cache")
	cmd.Env = append(filterInheritedEnv(os.Environ()),
		"DSH_HOME="+dshHome,
		"NARB_NATIVE_CACHE_DIR="+nativeCacheDir,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 dsh 失败: %w", err)
	}

	process := &Process{cmd: cmd, logger: logger}

	job, err := createKillOnCloseJob()
	if err != nil {
		logger.Printf("创建 Job 对象失败: %v", err)
	} else if err := assignToJob(job, cmd.Process.Pid); err != nil {
		logger.Printf("将 dsh 加入 Job 对象失败: %v", err)
		syscall.CloseHandle(job)
	} else {
		process.job = job
	}

	logger.Printf("已启动 dsh（PID %d）", cmd.Process.Pid)
	return process, nil
}

// Wait 阻塞到子进程退出并返回其退出状态（可重复调用）。
func (p *Process) Wait() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
	})
	return p.waitErr
}

// Stop 结束 dsh：关闭 Job 句柄触发 kill-on-close 终止整棵进程树，并回收
// 直接子进程。可重复调用。
func (p *Process) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		if p.job != 0 {
			syscall.CloseHandle(p.job)
			p.job = 0
		}
		done := make(chan struct{})
		go func() {
			_ = p.Wait()
			close(done)
		}()
		select {
		case <-done:
			p.logger.Printf("dsh 已退出")
		case <-time.After(5 * time.Second):
			p.logger.Printf("dsh 未在超时内退出，强制结束")
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			<-done
		}
	})
}

// WaitReady 轮询 url 直到 HTTP 服务可访问或超时。
func WaitReady(url string, timeout time.Duration, logger *runlog.Logger) error {
	logger.Printf("等待 %s 就绪", url)
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			logger.Printf("%s 已就绪", url)
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 超时", url)
}

func createKillOnCloseJob() (syscall.Handle, error) {
	handle, _, err := procCreateJobObjectW.Call(0, 0)
	if handle == 0 {
		return 0, fmt.Errorf("CreateJobObjectW: %v", err)
	}
	info := jobObjectExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	result, _, err := procSetInformationJobObject.Call(
		handle,
		jobObjectExtendedLimitInfoClass,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if result == 0 {
		syscall.CloseHandle(syscall.Handle(handle))
		return 0, fmt.Errorf("SetInformationJobObject: %v", err)
	}
	return syscall.Handle(handle), nil
}

func assignToJob(job syscall.Handle, pid int) error {
	processHandle, _, err := procOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(pid))
	if processHandle == 0 {
		return fmt.Errorf("OpenProcess: %v", err)
	}
	defer procCloseHandle.Call(processHandle)
	result, _, err := procAssignProcessToJobObject.Call(uintptr(job), processHandle)
	if result == 0 {
		return fmt.Errorf("AssignProcessToJobObject: %v", err)
	}
	return nil
}
