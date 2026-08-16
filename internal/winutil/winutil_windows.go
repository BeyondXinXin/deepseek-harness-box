// Package winutil 提供 Windows 平台相关的系统调用封装。
package winutil

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

const (
	errorAlreadyExists syscall.Errno = 183

	// MessageBox 图标 / 按钮标志。
	MBOK              = 0x00000000
	MBYesNo           = 0x00000004
	MBIconError       = 0x00000010
	MBIconWarning     = 0x00000030
	MBIconInformation = 0x00000040
	IDYes             = 6
	IDNo              = 7

	// TaskDialog 图标（MAKEINTRESOURCE 形式的资源 ID）。
	TDWarningIcon = 0xFFFE
	TDErrorIcon   = 0xFFFF
	TDInfoIcon    = 0xFFFD

	// TDF_ALLOW_DIALOG_CANCELLATION：允许 Esc / 标题栏 X 关闭对话框，
	// 关闭时返回 IDCANCEL(2)。
	tdAllowDialogCancellation = 0x0008

	// 剪贴板：Unicode 文本格式与可移动全局内存标志。
	cfUnicodeText = 13
	gMemMoveable  = 0x0002
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	comctl32         = syscall.NewLazyDLL("comctl32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
	procMessageBoxW  = user32.NewProc("MessageBoxW")

	// 剪贴板相关。
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procCloseClipboard   = user32.NewProc("CloseClipboard")

	procGlobalAlloc   = kernel32.NewProc("GlobalAlloc")
	procGlobalLock    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock  = kernel32.NewProc("GlobalUnlock")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")

	// TaskDialogIndirect 是 comctl32 v6 提供的任务对话框 API（Vista+），
	// 支持自定义按钮文字与折叠详情，比 MessageBox 更适合面向小白用户。
	procTaskDialogIndirect = comctl32.NewProc("TaskDialogIndirect")
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

// SetClipboardText 把文本以 Unicode 格式写入系统剪贴板。
func SetClipboardText(text string) error {
	encoded, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	if result, _, _ := procOpenClipboard.Call(0); result == 0 {
		return errors.New("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()
	if result, _, _ := procEmptyClipboard.Call(); result == 0 {
		return errors.New("EmptyClipboard failed")
	}

	size := uintptr(len(encoded) * 2)
	handle, _, callErr := procGlobalAlloc.Call(gMemMoveable, size)
	if handle == 0 {
		return fmt.Errorf("GlobalAlloc failed: %v", callErr)
	}
	// SetClipboardData 成功后内存所有权移交给系统，不得再 GlobalFree。
	transferred := false
	defer func() {
		if !transferred {
			procGlobalFree.Call(handle)
		}
	}()

	locked, _, callErr := procGlobalLock.Call(handle)
	if locked == 0 {
		return fmt.Errorf("GlobalLock failed: %v", callErr)
	}
	// 用 RtlMoveMemory 写入锁定内存：dst/size 都是纯 uintptr 参数，避免
	// uintptr → unsafe.Pointer 回转换（go vet unsafeptr 检查会拦截该模式）。
	procRtlMoveMemory.Call(locked, uintptr(unsafe.Pointer(&encoded[0])), uintptr(len(encoded)*2))
	_, _, _ = procGlobalUnlock.Call(handle)

	if result, _, _ := procSetClipboardData.Call(cfUnicodeText, handle); result == 0 {
		return errors.New("SetClipboardData failed")
	}
	transferred = true
	return nil
}

// taskDialogConfig 对应 Windows TASKDIALOGCONFIG 结构，字段顺序与官方头文件
// 完全一致（MainIcon/FooterIcon 为联合体，按 uintptr 处理），不能随意调整。
type taskDialogConfig struct {
	CbSize                  uint32
	HwndParent              uintptr
	HInstance               uintptr
	DwFlags                 uint32
	DwCommonButtons         uint32
	PszWindowTitle          *uint16
	MainIcon                uintptr
	PszMainInstruction      *uint16
	PszContent              *uint16
	CButtons                uint32
	PButtons                *taskDialogButton
	NDefaultButton          int32
	CRadioButtons           uint32
	PRadioButtons           *taskDialogButton
	NDefaultRadioButton     int32
	PszVerificationText     *uint16
	PszExpandedInformation  *uint16
	PszExpandedControlText  *uint16
	PszCollapsedControlText *uint16
	FooterIcon              uintptr
	PszFooter               *uint16
	PfCallback              uintptr
	LpCallbackData          uintptr
	CxWidth                 uint32
}

// taskDialogButton 对应 Windows TASKDIALOG_BUTTON 结构。
type taskDialogButton struct {
	NButtonID     int32
	PszButtonText *uint16
}

// TaskDialogButton 是任务对话框上的一个自定义按钮。
type TaskDialogButton struct {
	ID    int
	Label string
}

// TaskDialog 显示带自定义按钮的 Windows 任务对话框。title 为窗口标题，main
// 为主提示语，content 为正文；buttons 依次排列，defaultIndex 为默认按钮下标
// （-1 表示无默认）；icon 取 TDErrorIcon / TDWarningIcon / TDInfoIcon 之一；
// expanded 为折叠的详细信息，用户点「查看错误详情」才展开。返回被点击按钮
// 的 ID，对话框被关闭或取消时返回 0。
func TaskDialog(title, main, content string, buttons []TaskDialogButton, defaultIndex int, icon uint32, expanded string) int {
	titlePointer, _ := syscall.UTF16PtrFromString(title)
	mainPointer, _ := syscall.UTF16PtrFromString(main)
	contentPointer, _ := syscall.UTF16PtrFromString(content)

	var expandedPointer, expandLabel, collapseLabel *uint16
	if expanded != "" {
		expandedPointer, _ = syscall.UTF16PtrFromString(expanded)
		expandLabel, _ = syscall.UTF16PtrFromString("查看错误详情")
		collapseLabel, _ = syscall.UTF16PtrFromString("收起错误详情")
	}

	taskButtons := make([]taskDialogButton, len(buttons))
	for i, b := range buttons {
		labelPointer, _ := syscall.UTF16PtrFromString(b.Label)
		taskButtons[i] = taskDialogButton{NButtonID: int32(b.ID), PszButtonText: labelPointer}
	}
	var pButtons *taskDialogButton
	if len(taskButtons) > 0 {
		pButtons = &taskButtons[0]
	}

	config := taskDialogConfig{
		CbSize:                  uint32(unsafe.Sizeof(taskDialogConfig{})),
		DwFlags:                 tdAllowDialogCancellation,
		PszWindowTitle:          titlePointer,
		MainIcon:                uintptr(icon),
		PszMainInstruction:      mainPointer,
		PszContent:              contentPointer,
		CButtons:                uint32(len(taskButtons)),
		PButtons:                pButtons,
		NDefaultButton:          int32(defaultIndex),
		PszExpandedInformation:  expandedPointer,
		PszExpandedControlText:  expandLabel,
		PszCollapsedControlText: collapseLabel,
	}

	var clicked int32
	result, _, _ := procTaskDialogIndirect.Call(
		uintptr(unsafe.Pointer(&config)),
		uintptr(unsafe.Pointer(&clicked)),
		0, 0,
	)
	if result != 0 || clicked == 0 {
		// TaskDialogIndirect 调用失败（极少见，如异常精简的系统）：降级为
		// 普通消息框。两个按钮时映射到「是/否」，其余情况仅提示并返回
		// 第一个按钮。
		fallback := main + "\r\n\r\n" + content
		if len(buttons) == 2 {
			choice := MessageBox(0, title, fallback+"\r\n\r\n是："+buttons[0].Label+"\r\n否："+buttons[1].Label, MBYesNo|MBIconError)
			if choice == IDYes {
				return buttons[0].ID
			}
			return buttons[1].ID
		}
		MessageBox(0, title, fallback, MBOK)
		if len(buttons) > 0 {
			return buttons[0].ID
		}
		return 0
	}
	// 用户按 Esc 或点标题栏 X 取消时返回 IDCANCEL(2)，同样视为关闭。
	if clicked == 2 {
		return 0
	}
	return int(clicked)
}
