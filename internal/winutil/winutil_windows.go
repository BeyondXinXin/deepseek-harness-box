// Package winutil 提供 Windows 平台相关的系统调用封装。
package winutil

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

const (
	errorAlreadyExists syscall.Errno = 183

	// MessageBox 图标 / 按钮标志。
	MBOK               = 0x00000000
	MBIconError        = 0x00000010
	MBIconWarning      = 0x00000030
	MBIconInformation  = 0x00000040
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
	procMessageBoxW  = user32.NewProc("MessageBoxW")
)

// SingleInstance 持有进程级互斥体句柄，用于防止重复启动。
type SingleInstance struct {
	handle uintptr
	once   sync.Once
}

// AcquireSingleInstance 创建（或检测）一个命名互斥体。返回的 alreadyRunning
// 为 true 表示已有实例在运行。
func AcquireSingleInstance(name string) (*SingleInstance, bool, error) {
	namePointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePointer)))
	if handle == 0 {
		if callErr != syscall.Errno(0) {
			return nil, false, callErr
		}
		return nil, false, fmt.Errorf("CreateMutexW failed")
	}
	if callErr == errorAlreadyExists {
		_, _, _ = procCloseHandle.Call(handle)
		return nil, true, nil
	}
	return &SingleInstance{handle: handle}, false, nil
}

// Close 释放互斥体句柄。
func (instance *SingleInstance) Close() error {
	if instance == nil || instance.handle == 0 {
		return nil
	}
	var closeErr error
	instance.once.Do(func() {
		result, _, callErr := procCloseHandle.Call(instance.handle)
		if result == 0 && callErr != syscall.Errno(0) {
			closeErr = callErr
		}
		instance.handle = 0
	})
	return closeErr
}

// HideWindow 让 cmd 启动的子进程不弹出控制台黑窗口。
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// MessageBox 弹出简单消息框。owner 传 0 表示无父窗口。
func MessageBox(owner uintptr, title, text string, flags uint32) int {
	titlePointer, _ := syscall.UTF16PtrFromString(title)
	textPointer, _ := syscall.UTF16PtrFromString(text)
	result, _, _ := procMessageBoxW.Call(
		owner,
		uintptr(unsafe.Pointer(textPointer)),
		uintptr(unsafe.Pointer(titlePointer)),
		uintptr(flags),
	)
	return int(result)
}
