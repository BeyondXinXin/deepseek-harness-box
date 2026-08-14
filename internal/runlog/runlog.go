// Package runlog 提供追加式文件日志。
package runlog

import (
	"log"
	"os"
	"path/filepath"
)

// Logger 将运行日志追加写入日志目录下的固定文件。
type Logger struct {
	Path string
	file *os.File
	log  *log.Logger
}

// Open 在 logsDir 下打开（或创建）HarnessBox.log 并返回追加式日志器。
func Open(logsDir string) (*Logger, error) {
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(logsDir, "HarnessBox.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &Logger{Path: path, file: file, log: log.New(file, "", log.LstdFlags)}, nil
}

// Printf 追加一条日志。
func (l *Logger) Printf(format string, args ...any) {
	if l == nil || l.log == nil {
		return
	}
	l.log.Printf(format, args...)
}

// Close 关闭底层日志文件。
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}
