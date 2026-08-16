package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRedact 验证脱敏：各种形式的 Key/token/密码值必须被替换，普通内容
// 保持原样。
func TestRedact(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantSub []string // 脱敏后必须仍出现的子串
		notWant []string // 脱敏后绝对不能出现的子串
	}{
		{
			name:    "JSON 键值",
			in:      `{"apiKey":"sk-abc123def456","other":"keep"}`,
			wantSub: []string{`"apiKey":"***"`, `"other":"keep"`},
			notWant: []string{"sk-abc123def456"},
		},
		{
			name:    "环境变量形式",
			in:      "DEEPSEEK_API_KEY=sk-abc123def456",
			wantSub: []string{"DEEPSEEK_API_KEY=***"},
			notWant: []string{"sk-abc123def456"},
		},
		{
			name:    "冒号分隔",
			in:      "secret: abc123",
			wantSub: []string{"secret:***"},
			notWant: []string{"abc123"},
		},
		{
			name:    "裸 sk 密钥",
			in:      "启动失败 sk-abc123def456 请检查",
			wantSub: []string{"sk-***"},
			notWant: []string{"sk-abc123def456"},
		},
		{
			name:    "Bearer token",
			in:      "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abc",
			wantSub: []string{"Bearer ***"},
			notWant: []string{"eyJhbGciOiJIUzI1NiJ9"},
		},
		{
			name:    "普通日志不受影响",
			in:      "已启动 dsh（PID 1234）\n端口 3081 被 node.exe 占用",
			wantSub: []string{"已启动 dsh（PID 1234）", "端口 3081 被 node.exe 占用"},
			notWant: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Redact(test.in)
			for _, sub := range test.wantSub {
				if !strings.Contains(got, sub) {
					t.Fatalf("Redact(%q) = %q, 缺少 %q", test.in, got, sub)
				}
			}
			for _, sub := range test.notWant {
				if strings.Contains(got, sub) {
					t.Fatalf("Redact(%q) = %q, 仍包含敏感内容 %q", test.in, got, sub)
				}
			}
		})
	}
}

// TestTailLog 验证只取最后 50 行且保持顺序。
func TestTailLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "line %02d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	lines := tailLog(path)
	if len(lines) != tailLines {
		t.Fatalf("tailLog 行数 = %d, want %d", len(lines), tailLines)
	}
	if lines[0] != "line 11" {
		t.Fatalf("第一行 = %q, want %q", lines[0], "line 11")
	}
	if lines[len(lines)-1] != "line 60" {
		t.Fatalf("最后一行 = %q, want %q", lines[len(lines)-1], "line 60")
	}

	if lines := tailLog(filepath.Join(dir, "missing.log")); lines != nil {
		t.Fatalf("不存在的日志应返回 nil, got %v", lines)
	}
	if lines := tailLog(""); lines != nil {
		t.Fatalf("空路径应返回 nil, got %v", lines)
	}
}

// TestCollect 验证诊断报告包含全部必需字段，且日志中的 Key 被脱敏。
func TestCollect(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	logContent := "启动失败: 端口被占用\nDEEPSEEK_API_KEY=sk-abc123def456\n"
	if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "node"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "node", "node.exe"), []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}
	payloadMeta := `{"version":"v0.1.0","node":"node-v24.19.0","dsh":"2.1.0"}`
	if err := os.WriteFile(filepath.Join(runtimeDir, "payload.json"), []byte(payloadMeta), 0644); err != nil {
		t.Fatal(err)
	}

	report := Collect(Context{LogPath: logPath, Port: 3081, RuntimeDir: runtimeDir}, "启动失败")

	for _, want := range []string{
		"DeepSeekHarnessBox 诊断信息",
		"HarnessBox 版本：",
		"DSH 版本：2.1.0（Node node-v24.19.0）",
		"Windows 版本：",
		"当前端口：3081",
		"Node 是否存在：存在",
		"dsh 是否存在：不存在",
		"错误信息：启动失败",
		"最近日志（最多 2 行）",
		"DEEPSEEK_API_KEY=***",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("诊断报告缺少 %q，完整报告：\n%s", want, report)
		}
	}
	if strings.Contains(report, "sk-abc123def456") {
		t.Errorf("诊断报告泄露 API Key，完整报告：\n%s", report)
	}
}

// TestCollectMissingRuntime 验证运行环境未释放时字段降级为「未知」。
func TestCollectMissingRuntime(t *testing.T) {
	report := Collect(Context{}, "")
	for _, want := range []string{
		"DSH 版本：未知（运行环境未释放）",
		"当前端口：未确定",
		"Node 是否存在：未知（运行环境未释放）",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("诊断报告缺少 %q，完整报告：\n%s", want, report)
		}
	}
}
