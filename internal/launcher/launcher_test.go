package launcher

import (
	"reflect"
	"testing"
)

// TestSplitCredentialEnv 验证从继承环境中拆分 API Key 凭据变量：凡名称以
// _API_KEY 结尾的变量（大小写均算，Windows 环境变量名不区分大小写）进入
// 凭据列表，其余变量原样保留。
func TestSplitCredentialEnv(t *testing.T) {
	tests := []struct {
		name        string
		in          []string
		wantPlain   []string
		wantCreds   []string
	}{
		{
			name:      "无凭据变量时原样保留",
			in:        []string{"PATH=C:\\Windows", "TEMP=C:\\Temp"},
			wantPlain: []string{"PATH=C:\\Windows", "TEMP=C:\\Temp"},
			wantCreds: nil,
		},
		{
			name:      "剔除大写 API Key",
			in:        []string{"PATH=C:\\Windows", "DEEPSEEK_API_KEY=sk-abc", "TEMP=C:\\Temp"},
			wantPlain: []string{"PATH=C:\\Windows", "TEMP=C:\\Temp"},
			wantCreds: []string{"DEEPSEEK_API_KEY"},
		},
		{
			name:      "剔除小写 API Key",
			in:        []string{"PATH=C:\\Windows", "deepseek_api_key=sk-abc"},
			wantPlain: []string{"PATH=C:\\Windows"},
			wantCreds: []string{"deepseek_api_key"},
		},
		{
			name:      "剔除其他提供方 API Key",
			in:        []string{"PATH=C:\\Windows", "ANTHROPIC_API_KEY=sk-a", "OPENAI_API_KEY=sk-o"},
			wantPlain: []string{"PATH=C:\\Windows"},
			wantCreds: []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"},
		},
		{
			name:      "不误伤名称相似或无关的其他变量",
			in:        []string{"DEEPSEEK_API_KEYS=keep", "API_KEY_VALUE=keep", "GITHUB_TOKEN=keep"},
			wantPlain: []string{"DEEPSEEK_API_KEYS=keep", "API_KEY_VALUE=keep", "GITHUB_TOKEN=keep"},
			wantCreds: nil,
		},
		{
			name:      "空列表",
			in:        nil,
			wantPlain: nil,
			wantCreds: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plain, credentials := splitCredentialEnv(test.in)
			if !reflect.DeepEqual(plain, test.wantPlain) {
				t.Fatalf("splitCredentialEnv(%v) plain = %v, want %v", test.in, plain, test.wantPlain)
			}
			if !reflect.DeepEqual(credentials, test.wantCreds) {
				t.Fatalf("splitCredentialEnv(%v) credentials = %v, want %v", test.in, credentials, test.wantCreds)
			}
		})
	}
}
