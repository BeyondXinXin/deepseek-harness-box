package shortcut

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCreateShellLink 实际调用 COM 创建快捷方式，验证 VTable 槽位顺序
// 正确、Save 成功并产出非空 .lnk 文件。
func TestCreateShellLink(t *testing.T) {
	target, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	linkPath := filepath.Join(t.TempDir(), "test.lnk")
	if err := createShellLink(linkPath, target, "test"); err != nil {
		t.Fatalf("createShellLink: %v", err)
	}
	info, err := os.Stat(linkPath)
	if err != nil {
		t.Fatalf("快捷方式未创建: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("快捷方式文件为空")
	}
}
