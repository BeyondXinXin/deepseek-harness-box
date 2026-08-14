// Package tray 提供 Windows 通知区域图标（托盘），作为 HarnessBox 的唯一
// 界面：右键菜单可打开浏览器或退出，双击图标打开浏览器。
package tray

import (
	"errors"
	"syscall"
	"unsafe"
)

// Actions 是托盘回调动作。
type Actions struct {
	OnOpen func()
}

const (
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	wmLButtonDblClk = 0x0203
	wmDestroy       = 0x0002

	nimAdd            = 0x00000000
	nimDelete         = 0x00000002
	nimSetVersion     = 0x00000004
	nifMessage        = 0x00000001
	nifIcon           = 0x00000002
	nifTip            = 0x00000004
	// 与 lxn/walk（ModelPilot 同款）一致使用版本 3：lParam 即鼠标消息，
	// 语义简单稳定；版本 4 的 lParam 低字为 Shell 通知事件、高字为图标
	// ID，不同 Windows 版本投递行为不一致。
	notifyIconVersion = 3

	imageIcon     = 1
	lrDefaultSize = 0x0040

	// rsrc（见 build.cmd）按顺序分配资源 ID：manifest 为 1，图标组
	// RT_GROUP_ICON 为 2，各尺寸的单个 RT_ICON 为 3 起。LoadImage 的
	// IMAGE_ICON 按图标组查找，因此这里必须传 2；传 1 会命中 manifest，
	// 加载失败导致托盘图标空白。
	groupIconResourceID = 2
	// IDI_APPLICATION：系统默认应用图标，作为图标资源缺失时的兜底。
	idiApplication = 32512

	mfString    = 0x00000000
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmNonotify    = 0x0080

	menuOpen = 1001
	menuExit = 1002

	// 托盘鼠标事件通过该自定义消息投递到窗口。
	trayCallbackMessage = 0x8000 + 100
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procUnregisterClassW       = user32.NewProc("UnregisterClassW")
	procShellNotifyIconW       = shell32.NewProc("Shell_NotifyIconW")
	procLoadImageW             = user32.NewProc("LoadImageW")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procSendMessageW           = user32.NewProc("SendMessageW")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
)

type point struct {
	x int32
	y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type tray struct {
	actions    Actions
	hwnd       uintptr
	hInstance  uintptr
	className  string
	classNameP *uint16
	nid        notifyIconData
}

var (
	active            *tray
	taskbarCreatedMsg uint32
)

// Run 创建托盘图标并进入消息循环，直到用户选择「退出」。
func Run(actions Actions) error {
	if active != nil {
		return errors.New("托盘已在运行")
	}

	hInstance, _, _ := procGetModuleHandleW.Call(0)
	if hInstance == 0 {
		return errors.New("GetModuleHandleW failed")
	}

	taskbarCreatedPtr, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	registered, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarCreatedPtr)))
	taskbarCreatedMsg = uint32(registered)

	t := &tray{
		actions:   actions,
		hInstance: hInstance,
		className: "BeyondXinXin.HarnessBox.Tray",
	}
	active = t

	classNamePtr, err := syscall.UTF16PtrFromString(t.className)
	if err != nil {
		active = nil
		return err
	}
	t.classNameP = classNamePtr

	icon := loadIcon()
	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hInstance,
		hIcon:         icon,
		hCursor:       0,
		hbrBackground: 0,
		lpszClassName: classNamePtr,
		hIconSm:       icon,
	}
	if atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		active = nil
		return errors.New("RegisterClassExW failed")
	}

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(classNamePtr)),
		0, // lpWindowName
		0, // dwStyle：隐藏窗口
		0, 0, 0, 0,
		0, 0,
		hInstance,
		0,
	)
	if hwnd == 0 {
		active = nil
		return errors.New("CreateWindowExW failed")
	}
	t.hwnd = hwnd

	t.nid = notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             hwnd,
		uID:              0, // 与 lxn/walk 一致：uID 为 0，图标事件由 hWnd 定位
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: trayCallbackMessage,
		hIcon:            icon,
	}
	copy(t.nid.szTip[:], utf16FromString("HarnessBox"))
	if added, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid))); added == 0 {
		procDestroyWindow.Call(hwnd)
		active = nil
		return errors.New("Shell_NotifyIconW NIM_ADD failed")
	}
	t.nid.uVersion = notifyIconVersion
	_, _, _ = procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&t.nid)))

	messageLoop()

	removeIcon(t)
	procDestroyWindow.Call(t.hwnd)
	procUnregisterClassW.Call(uintptr(unsafe.Pointer(classNamePtr)), hInstance)
	active = nil
	return nil
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	msgID := uint32(message)
	switch msgID {
	case trayCallbackMessage:
		// 版本 3 下 lParam 即鼠标消息，wParam 为图标 ID（uID=0）；取低
		// 16 位比较以兼容个别 Shell 实现把 ID 放进高字的行为。
		switch uint16(lParam) {
		case wmRButtonUp:
			// 与 lxn/walk 相同：把右键统一重投递为 WM_CONTEXTMENU，
			// 使鼠标右键与键盘菜单键走同一条弹出菜单路径。
			procSendMessageW.Call(hwnd, trayCallbackMessage, wParam, wmContextMenu)
		case wmContextMenu:
			showMenu(hwnd)
		case wmLButtonDblClk:
			openAction()
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	if taskbarCreatedMsg != 0 && msgID == taskbarCreatedMsg {
		if active != nil {
			_, _, _ = procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&active.nid)))
			_, _, _ = procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&active.nid)))
		}
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return result
}

func showMenu(hwnd uintptr) {
	var cursor point
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))

	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	openPtr, _ := syscall.UTF16PtrFromString("打开 HarnessBox")
	exitPtr, _ := syscall.UTF16PtrFromString("退出")
	_, _, _ = procAppendMenuW.Call(menu, mfString, menuOpen, uintptr(unsafe.Pointer(openPtr)))
	_, _, _ = procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	_, _, _ = procAppendMenuW.Call(menu, mfString, menuExit, uintptr(unsafe.Pointer(exitPtr)))

	_, _, _ = procSetForegroundWindow.Call(hwnd)
	selected, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmRightButton|tpmReturnCmd|tpmNonotify,
		uintptr(cursor.x), uintptr(cursor.y),
		0, hwnd, 0,
	)
	_, _, _ = procPostMessageW.Call(hwnd, 0, 0, 0)

	switch selected {
	case menuOpen:
		openAction()
	case menuExit:
		removeIcon(active)
		procDestroyWindow.Call(hwnd)
	}
}

func openAction() {
	if active != nil && active.actions.OnOpen != nil {
		active.actions.OnOpen()
	}
}

func removeIcon(t *tray) {
	if t == nil {
		return
	}
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
}

func loadIcon() uintptr {
	if active == nil {
		return 0
	}
	// 从本模块加载图标组（RT_GROUP_ICON，资源 ID 2），而非系统的
	// IDI_APPLICATION 通用图标。
	icon, _, _ := procLoadImageW.Call(active.hInstance, groupIconResourceID, imageIcon, 0, 0, lrDefaultSize)
	if icon == 0 {
		// 兜底：图标资源缺失时退回系统默认应用图标，避免托盘图标空白。
		icon, _, _ = procLoadImageW.Call(0, idiApplication, imageIcon, 0, 0, lrDefaultSize)
	}
	return icon
}

func messageLoop() {
	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func utf16FromString(s string) []uint16 {
	encoded, _ := syscall.UTF16FromString(s)
	return encoded
}
