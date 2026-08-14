// Package shortcut 通过 COM 的 IShellLinkW 接口在用户桌面创建快捷方式，
// 不依赖任何第三方库。
package shortcut

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	clsctxInprocServer = 0x1

	coinitApartmentThreaded = 0x2

	// CSIDL_DESKTOPDIRECTORY：用户桌面的物理路径。
	csidlDesktop = 0x0010

	// COM 返回码。
	sOK    = 0
	sFalse = 1
)

var (
	ole32   = syscall.NewLazyDLL("ole32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSHGetFolderPathW = shell32.NewProc("SHGetFolderPathW")

	// CLSID_ShellLink：{00021401-0000-0000-C000-000000000046}
	clsidShellLink = syscall.GUID{
		Data1: 0x00021401,
		Data2: 0x0000,
		Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	// IID_IShellLinkW：{000214F9-0000-0000-C000-000000000046}
	iidIShellLinkW = syscall.GUID{
		Data1: 0x000214F9,
		Data2: 0x0000,
		Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	// IID_IPersistFile：{0000010B-0000-0000-C000-000000000046}
	iidIPersistFile = syscall.GUID{
		Data1: 0x0000010B,
		Data2: 0x0000,
		Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
)

// ishellLink 是 IShellLinkW 接口视图，仅声明本包用到的方法及其之前的
// 槽位；顺序必须与 MSDN 定义的 VTable 保持一致（IUnknown 3 个 + IShellLink
// 18 个）。
type ishellLink struct {
	vtbl *ishellLinkVtbl
}

type ishellLinkVtbl struct {
	queryInterface      uintptr
	addRef              uintptr
	release             uintptr
	getPath             uintptr
	getIDList           uintptr
	setIDList           uintptr
	getDescription      uintptr
	setDescription      uintptr
	getWorkingDirectory uintptr
	setWorkingDirectory uintptr
	getArguments         uintptr
	setArguments         uintptr
	getHotkey            uintptr
	setHotkey            uintptr
	getShowCmd           uintptr
	setShowCmd           uintptr
	getIconLocation      uintptr
	setIconLocation      uintptr
	setRelativePath      uintptr
	resolve              uintptr
	setPath              uintptr
}

// iPersistFile 是 IPersistFile 接口视图，仅声明到 Save 为止。
type iPersistFile struct {
	vtbl *iPersistFileVtbl
}

type iPersistFileVtbl struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	getClassID     uintptr
	isDirty        uintptr
	load           uintptr
	save           uintptr
	saveCompleted  uintptr
	getCurFile     uintptr
}

// Create 在用户桌面创建名为 name（含 .lnk 后缀）的快捷方式，指向
// targetPath。桌面已存在同名文件时不覆盖，原样返回其路径。
func Create(name, targetPath, description string) (string, error) {
	if _, err := os.Stat(targetPath); err != nil {
		return "", fmt.Errorf("快捷方式目标不存在: %w", err)
	}
	desktop, err := DesktopDir()
	if err != nil {
		return "", err
	}
	linkPath := filepath.Join(desktop, name)
	if _, err := os.Stat(linkPath); err == nil {
		return linkPath, nil
	}
	if err := createShellLink(linkPath, targetPath, description); err != nil {
		return "", err
	}
	return linkPath, nil
}

// DesktopDir 返回用户桌面目录。
func DesktopDir() (string, error) {
	var buffer [syscall.MAX_PATH]uint16
	hr, _, _ := procSHGetFolderPathW.Call(0, csidlDesktop, 0, 0, uintptr(unsafe.Pointer(&buffer[0])))
	if hr != sOK {
		return "", fmt.Errorf("SHGetFolderPathW failed: 0x%X", hr)
	}
	return syscall.UTF16ToString(buffer[:]), nil
}

func createShellLink(linkPath, targetPath, description string) error {
	// COM 需要在 STA 线程使用；重复初始化返回 S_FALSE 属正常。
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if hr != sOK && hr != sFalse {
		return fmt.Errorf("CoInitializeEx failed: 0x%X", hr)
	}
	defer procCoUninitialize.Call()

	// CoCreateInstance 会把 IShellLinkW 接口指针写入 link。
	var link *ishellLink
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIShellLinkW)),
		uintptr(unsafe.Pointer(&link)),
	)
	if hr != sOK {
		return fmt.Errorf("CoCreateInstance(IShellLinkW) failed: 0x%X", hr)
	}
	defer syscall.SyscallN(link.vtbl.release, uintptr(unsafe.Pointer(link)))

	targetPtr, err := syscall.UTF16PtrFromString(targetPath)
	if err != nil {
		return err
	}
	if hr, _, _ := syscall.SyscallN(link.vtbl.setPath, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(targetPtr))); hr != sOK {
		return fmt.Errorf("IShellLinkW.SetPath failed: 0x%X", hr)
	}

	descPtr, err := syscall.UTF16PtrFromString(description)
	if err != nil {
		return err
	}
	if hr, _, _ := syscall.SyscallN(link.vtbl.setDescription, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(descPtr))); hr != sOK {
		return fmt.Errorf("IShellLinkW.SetDescription failed: 0x%X", hr)
	}

	// 图标取目标程序自带的第一个图标。
	if hr, _, _ := syscall.SyscallN(link.vtbl.setIconLocation, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(targetPtr)), 0); hr != sOK {
		return fmt.Errorf("IShellLinkW.SetIconLocation failed: 0x%X", hr)
	}

	// 工作目录指向目标所在目录，避免相对资源加载失败。
	workingPtr, err := syscall.UTF16PtrFromString(filepath.Dir(targetPath))
	if err != nil {
		return err
	}
	if hr, _, _ := syscall.SyscallN(link.vtbl.setWorkingDirectory, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(workingPtr))); hr != sOK {
		return fmt.Errorf("IShellLinkW.SetWorkingDirectory failed: 0x%X", hr)
	}

	// QueryInterface 会把 IPersistFile 接口指针写入 persist。
	var persist *iPersistFile
	if hr, _, _ := syscall.SyscallN(link.vtbl.queryInterface, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(&iidIPersistFile)), uintptr(unsafe.Pointer(&persist))); hr != sOK {
		return fmt.Errorf("QueryInterface(IPersistFile) failed: 0x%X", hr)
	}
	defer syscall.SyscallN(persist.vtbl.release, uintptr(unsafe.Pointer(persist)))

	linkPathPtr, err := syscall.UTF16PtrFromString(linkPath)
	if err != nil {
		return err
	}
	// 最后一个参数 fRemember=TRUE：把快捷方式路径写入快捷方式自身。
	if hr, _, _ := syscall.SyscallN(persist.vtbl.save, uintptr(unsafe.Pointer(persist)), uintptr(unsafe.Pointer(linkPathPtr)), 1); hr != sOK {
		return fmt.Errorf("IPersistFile.Save failed: 0x%X", hr)
	}
	return nil
}
