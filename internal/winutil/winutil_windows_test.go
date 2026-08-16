package winutil

import (
	"runtime"
	"testing"
	"unsafe"
)

// TestTaskDialogStructLayout 校验 TASKDIALOGCONFIG / TASKDIALOG_BUTTON 结构体
// 尺寸与 Windows 官方头文件在 amd64 下的布局一致，防止字段顺序或类型调整
// 破坏 ABI 导致 TaskDialogIndirect 失效。
func TestTaskDialogStructLayout(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("仅校验 amd64 布局")
	}
	if size := unsafe.Sizeof(taskDialogConfig{}); size != 176 {
		t.Errorf("taskDialogConfig 尺寸 = %d，期望 176", size)
	}
	if size := unsafe.Sizeof(taskDialogButton{}); size != 16 {
		t.Errorf("taskDialogButton 尺寸 = %d，期望 16", size)
	}
}
