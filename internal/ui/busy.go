// Package ui 提供启动期的轻量提示窗口：首次初始化运行环境时在屏幕中央
// 显示「正在初始化」卡片并做省略号动画，避免用户误以为程序卡死。
package ui

import (
	"strings"
	"syscall"
	"unsafe"
)

const (
	wmTimer   = 0x0113
	wmDestroy = 0x0002
	wmSetFont = 0x0030
	// 后台任务完成时通过该消息通知窗口关闭。
	wmApp      = 0x8000
	wmWorkDone = wmApp + 1

	wsPopup   = 0x80000000
	wsBorder  = 0x00800000
	wsChild   = 0x40000000
	wsVisible = 0x10000000

	wsExTopmost = 0x00000008

	ssCenter      = 0x0001
	ssIcon        = 0x0003
	ssCenterImage = 0x0200

	swShow = 5

	stmSetIcon = 0x0170

	// COLOR_BTNFACE+1 作为窗口类背景刷子，与 STATIC 控件默认背景一致，
	// 避免文字控件与窗口背景出现色差。
	colorBtnFace = 15

	imageIcon = 1
	// rsrc（见 build.cmd）按顺序分配资源 ID：manifest 为 1，图标组
	// RT_GROUP_ICON 为 2，与托盘共用同一资源。
	groupIconResourceID = 2
	lrDefaultSize       = 0x0040
	// IDI_APPLICATION：图标资源缺失时兜底，也用作窗口光标（IDC_ARROW 同值）。
	idiApplication = 32512

	timerID         = 1
	timerIntervalMs = 400

	// 提示文字布局参数（px，96 DPI）。
	lineH = 26 // 单行文字高度（10.5pt 雅黑 + 行距）
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procUnregisterClassW = user32.NewProc("UnregisterClassW")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procSendMessageW     = user32.NewProc("SendMessageW")
	procSetWindowTextW   = user32.NewProc("SetWindowTextW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procSetTimer         = user32.NewProc("SetTimer")
	procKillTimer        = user32.NewProc("KillTimer")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procCreateFontW      = gdi32.NewProc("CreateFontW")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
)

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

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type point struct {
	x int32
	y int32
}

// state 保存当前提示窗口的绘制状态。同一时刻至多存在一个提示窗口。
type busyState struct {
	message string
	static  uintptr
	font    uintptr
	dots    int
}

var state *busyState

// RunBusy 显示一个置顶的「正在初始化」卡片窗口，并在后台线程执行 work；
// work 完成后窗口自动关闭并返回其结果。message 中的 \n 按多行显示。
// 窗口创建失败时降级为同步执行 work。
func RunBusy(message string, work func() error) error {
	hwnd := createWindow(message)
	if hwnd == 0 {
		return work()
	}

	result := make(chan error, 1)
	go func() {
		result <- work()
		_, _, _ = procPostMessageW.Call(hwnd, wmWorkDone, 0, 0)
	}()

	messageLoop()
	cleanup()
	return <-result
}

func createWindow(message string) uintptr {
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	if hInstance == 0 {
		return 0
	}
	className := "BeyondXinXin.DeepSeekHarnessBox.Busy"
	classNamePtr, err := syscall.UTF16PtrFromString(className)
	if err != nil {
		return 0
	}
	state = &busyState{message: message}

	cursor, _, _ := procLoadCursorW.Call(0, idiApplication)
	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hInstance,
		hCursor:       cursor,
		hbrBackground: colorBtnFace + 1,
		lpszClassName: classNamePtr,
	}
	if atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		state = nil
		return 0
	}

	width, height := measure(message)
	screenW, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	screenH, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	x := (int32(screenW) - int32(width)) / 2
	y := (int32(screenH) - int32(height)) / 2

	titlePtr, _ := syscall.UTF16PtrFromString("DeepSeekHarnessBox")
	hwnd, _, _ := procCreateWindowExW.Call(
		wsExTopmost,
		uintptr(unsafe.Pointer(classNamePtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		wsPopup|wsBorder,
		uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		state = nil
		return 0
	}

	staticClassPtr, _ := syscall.UTF16PtrFromString("STATIC")

	// 图标：优先本模块内嵌图标组（资源 ID 2），失败退回系统默认图标。
	// SS_CENTERIMAGE + 全宽控件让图标水平居中。
	icon, _, _ := procLoadImageW.Call(hInstance, groupIconResourceID, imageIcon, 0, 0, lrDefaultSize)
	if icon == 0 {
		icon, _, _ = procLoadImageW.Call(0, idiApplication, imageIcon, 0, 0, lrDefaultSize)
	}
	iconStatic, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(staticClassPtr)),
		0,
		wsChild|wsVisible|ssIcon|ssCenterImage,
		0, 18, uintptr(width), 32,
		hwnd, 0, hInstance, 0,
	)
	if iconStatic != 0 {
		_, _, _ = procSendMessageW.Call(iconStatic, stmSetIcon, icon, 0)
	}

	lineCount := len(strings.Split(message, "\n"))
	textPtr, _ := syscall.UTF16PtrFromString(message)
	state.static, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(staticClassPtr)),
		uintptr(unsafe.Pointer(textPtr)),
		wsChild|wsVisible|ssCenter,
		24, 62, uintptr(width-48), uintptr(lineCount*lineH),
		hwnd, 0, hInstance, 0,
	)
	// 自定义微软雅黑 10.5pt 字体：比 DEFAULT_GUI_FONT 更大更清晰，
	// 配合增大的行高避免两行文字挤在一起。
	state.font = createMessageFont()
	if state.static != 0 && state.font != 0 {
		_, _, _ = procSendMessageW.Call(state.static, wmSetFont, state.font, 1)
	}

	_, _, _ = procSetTimer.Call(hwnd, timerID, timerIntervalMs, 0)
	_, _, _ = procShowWindow.Call(hwnd, swShow)
	_, _, _ = procUpdateWindow.Call(hwnd)
	return hwnd
}

// measure 按消息文本估算窗口尺寸：图标区域 + 多行文字 + 上下留白。
func measure(message string) (int, int) {
	const (
		charW = 16 // 10.5pt 雅黑中文宽度（px，96 DPI 估算），留余量避免长文案贴边
		padX  = 24 // 左右留白
		iconH = 62 // 图标区域：18 上边距 + 32 图标 + 12 间距
		padY  = 18 // 底部留白
	)
	lines := strings.Split(message, "\n")
	maxLen := 0
	for _, line := range lines {
		if n := len([]rune(line)); n > maxLen {
			maxLen = n
		}
	}
	width := maxLen*charW + padX*2
	if width < 360 {
		width = 360
	}
	return width, iconH + len(lines)*lineH + padY
}

// createMessageFont 创建提示文字字体：Microsoft YaHei UI 10.5pt（负值
// 高度 -14px，@96 DPI），CLEARTYPE 抗锯齿，中文显示比默认字体更清晰。
func createMessageFont() uintptr {
	namePtr, err := syscall.UTF16PtrFromString("Microsoft YaHei UI")
	if err != nil {
		return 0
	}
	// 负高度表示字符高度（不含行距）：10.5pt @96 DPI。经变量转换再传
	// uintptr，避免常量负值转换溢出。
	fontHeight := int32(-14)
	font, _, _ := procCreateFontW.Call(
		uintptr(fontHeight), // cHeight
		0,                   // cWidth：由高度推导
		0, 0,                // cEscapement, cOrientation
		400,                 // cWeight：FW_NORMAL
		0, 0, 0,             // bItalic, bUnderline, bStrikeOut
		1,          // DEFAULT_CHARSET
		0, 0, 5, 0, // OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY, DEFAULT_PITCH|FF_DONTCARE
		uintptr(unsafe.Pointer(namePtr)),
	)
	return font
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch uint32(message) {
	case wmTimer:
		// 省略号动画（固定 3 个点的宽度，空格补齐，避免文字居中跳动），
		// 向用户表明程序仍在工作。
		if state != nil && state.static != 0 {
			state.dots = (state.dots + 1) % 4
			dots := strings.Repeat(".", state.dots) + strings.Repeat(" ", 3-state.dots)
			textPtr, _ := syscall.UTF16PtrFromString(state.message + dots)
			_, _, _ = procSetWindowTextW.Call(state.static, uintptr(unsafe.Pointer(textPtr)))
		}
		return 0
	case wmWorkDone:
		_, _, _ = procKillTimer.Call(hwnd, timerID)
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return result
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

func cleanup() {
	if state != nil && state.font != 0 {
		_, _, _ = procDeleteObject.Call(state.font)
	}
	classNamePtr, _ := syscall.UTF16PtrFromString("BeyondXinXin.DeepSeekHarnessBox.Busy")
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	_, _, _ = procUnregisterClassW.Call(uintptr(unsafe.Pointer(classNamePtr)), hInstance)
	state = nil
}
