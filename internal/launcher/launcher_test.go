package launcher

import (
	"reflect"
	"testing"
)

// TestFilterInheritedEnv 验证从继承环境中剔除 API Key 变量：大小写均被
// 剔除（Windows 环境变量名不区分大小写），其余变量保持原样。
func TestFilterInheritedEnv(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "无凭据变量时原样保留",
			in:   []string{"PATH=C:\\Windows", "TEMP=C:\\Temp"},
			want: []string{"PATH=C:\\Windows", "TEMP=C:\\Temp"},
		},
		{
			name: "剔除大写 API Key",
			in:   []string{"PATH=C:\\Windows", "DEEPSEEK_API_KEY=sk-abc", "TEMP=C:\\Temp"},
			want: []string{"PATH=C:\\Windows", "TEMP=C:\\Temp"},
		},
		{
			name: "剔除小写 API Key",
			in:   []string{"PATH=C:\\Windows", "deepseek_api_key=sk-abc"},
			want: []string{"PATH=C:\\Windows"},
		},
		{
			name: "不误伤名称相似的其他变量",
			in:   []string{"DEEPSEEK_API_KEYS=keep", "NOT_DEEPSEEK_API_KEY=keep"},
			want: []string{"DEEPSEEK_API_KEYS=keep", "NOT_DEEPSEEK_API_KEY=keep"},
		},
		{
			name: "空列表",
			in:   nil,
			want: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := filterInheritedEnv(test.in); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("filterInheritedEnv(%v) = %v, want %v", test.in, got, test.want)
			}
		})
	}
}
