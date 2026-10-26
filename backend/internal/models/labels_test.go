package models

import (
	"reflect"
	"testing"
)

// 标签是集合语义：去重、去空白，且保持首次出现顺序（顺序影响界面 chip 的排列）。
func TestNormalizeLabels(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"去重", []string{"docs", "docs"}, []string{"docs"}},
		{"保持顺序", []string{"b", "a", "b"}, []string{"b", "a"}},
		{"去空白项与空白字符", []string{" a ", "", "  ", "a"}, []string{"a"}},
		{"空入参返回 nil", nil, nil},
		{"全空白返回 nil", []string{" ", ""}, nil},
		{"原样保留正常标签", []string{"video", "hd"}, []string{"video", "hd"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeLabels(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("NormalizeLabels(%q) = %q, 期望 %q", c.in, got, c.want)
			}
		})
	}
}
