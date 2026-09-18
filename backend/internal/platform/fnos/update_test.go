package fnos

import "testing"

func TestNormalizeChangelog(t *testing.T) {
	// manifest 风格：<br> 分行 + 「更新日志」标题行，应转为纯文本换行并去标题
	in := "更新日志<br>v4.1.3.3:<br>1. transmission-daemon 改为 Alpine musl 全静态编译，去除 glibc 动态库依赖<br>2. WebUI 更换为基于 Go+React 的 trpanel 管理面板"
	want := "v4.1.3.3:\n1. transmission-daemon 改为 Alpine musl 全静态编译，去除 glibc 动态库依赖\n2. WebUI 更换为基于 Go+React 的 trpanel 管理面板"
	if got := normalizeChangelog(in); got != want {
		t.Fatalf("归一化结果不符:\n got: %q\nwant: %q", got, want)
	}

	// <br/> 与 <br /> 变体同样处理，HTML 实体需解码
	in2 := "更新日志<br/>v2:&lt;b&gt;加粗&lt;/b&gt;<br />说明"
	want2 := "v2:<b>加粗</b>\n说明"
	if got := normalizeChangelog(in2); got != want2 {
		t.Fatalf("br 变体处理不符:\n got: %q\nwant: %q", got, want2)
	}

	// 无标题时内容保持原样（仅去标签）
	in3 := "v3:<br>修复若干问题"
	if got := normalizeChangelog(in3); got != "v3:\n修复若干问题" {
		t.Fatalf("无标题场景不符: %q", got)
	}

	// 空内容返回空串
	if got := normalizeChangelog("   \n  "); got != "" {
		t.Fatalf("空白应返回空串: %q", got)
	}
}
