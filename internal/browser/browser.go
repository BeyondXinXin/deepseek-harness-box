// Package browser 负责用系统默认浏览器打开本地地址。
package browser

import (
	"fmt"
	"os/exec"
)

// Open 通过 shell 打开默认浏览器访问 url。
func Open(url string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开浏览器失败: %w", err)
	}
	return nil
}
