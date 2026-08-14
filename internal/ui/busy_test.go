package ui

import (
	"testing"
	"time"
)

// TestRunBusy 验证提示窗口能显示并随 work 完成自动关闭。
func TestRunBusy(t *testing.T) {
	err := RunBusy("正在初始化\n第二行文字", func() error {
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("RunBusy: %v", err)
	}
}
