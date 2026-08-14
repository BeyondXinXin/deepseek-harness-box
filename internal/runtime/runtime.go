// Package runtime 负责把内嵌 payload 释放到本地运行环境目录，并按版本
// 判断是否需要重新释放。
package runtime

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeyondXinXin/harnessbox/internal/runlog"
	"github.com/BeyondXinXin/harnessbox/payload"
)

const versionFile = ".version"

// NeedsExtract 报告运行环境是否需要（重新）释放：目录不存在或版本标记与
// 当前版本不一致。调用方据此决定是否展示初始化提示窗口。
func NeedsExtract(dir, version string) bool {
	return !upToDate(dir, version)
}

// Extract 确保 base 下的运行环境目录已释放为当前版本。版本未变化时跳过解压。
// 返回运行环境根目录（内含 node/ 与 dsh/）。
func Extract(base, version string, logger *runlog.Logger) (string, error) {
	dir := filepath.Join(base, "runtime")
	if upToDate(dir, version) {
		logger.Printf("运行环境已是最新（%s），跳过解压", version)
		return dir, nil
	}

	logger.Printf("释放运行环境到 %s（版本 %s）", dir, version)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("清理旧运行环境失败: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := extractZip(payload.Zip, dir); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, versionFile), []byte(version), 0644); err != nil {
		return "", fmt.Errorf("写入版本标记失败: %w", err)
	}
	return dir, nil
}

func upToDate(dir, version string) bool {
	data, err := os.ReadFile(filepath.Join(dir, versionFile))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == version
}

func extractZip(data []byte, dest string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("读取内嵌 payload 失败: %w", err)
	}
	root := filepath.Clean(dest) + string(os.PathSeparator)
	for _, file := range reader.File {
		name := filepath.FromSlash(file.Name)
		target := filepath.Join(dest, name)
		// 防 zip-slip：解压目标必须落在 dest 目录内。
		if !strings.HasPrefix(target, root) {
			return fmt.Errorf("payload 包含非法路径: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := extractFile(file, target); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(file *zip.File, target string) error {
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
