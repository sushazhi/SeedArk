package fnos

import (
	"strings"
	"testing"
)

// 下载顺序是用户可见行为：先直连 GitHub，失败再依次走两个 gh 代理。
// 顺序或域名被改回去（例如又变成代理优先、或换回旧代理）时这条会立刻失败。
func TestDownloadSourcesOrder(t *testing.T) {
	const fp = "https://github.com/o/r/releases/download/v1/x-amd64.fpk"
	srcs := downloadSources(fp)
	if len(srcs) != 3 {
		t.Fatalf("来源数量应为 3，实际 %d", len(srcs))
	}
	if srcs[0].url != fp || !srcs[0].direct {
		t.Fatalf("第一个来源应为 GitHub 直连，实际 %q direct=%v", srcs[0].url, srcs[0].direct)
	}
	if want := updateProxyMain + fp; srcs[1].url != want {
		t.Fatalf("第二个来源应为主代理 %q，实际 %q", want, srcs[1].url)
	}
	if want := updateProxyBackup + fp; srcs[2].url != want {
		t.Fatalf("第三个来源应为备用代理 %q，实际 %q", want, srcs[2].url)
	}
	// 代理来源一律不得标记为直连：downloadFPK 依赖它决定「上游未提供摘要」时是否可信
	for _, s := range srcs[1:] {
		if s.direct {
			t.Fatalf("代理来源不应标记为直连: %q", s.url)
		}
	}
	// 三条提示语各不相同，进度条上才能看出当前走的是哪条链路
	seen := map[string]bool{}
	for _, s := range srcs {
		if s.msg == "" || seen[s.msg] {
			t.Fatalf("来源提示语应非空且互不重复: %q", s.msg)
		}
		seen[s.msg] = true
	}
	if !strings.HasPrefix(srcs[1].url, "https://gh.dpik.top/") || !strings.HasPrefix(srcs[2].url, "https://v4.gh-proxy.org/") {
		t.Fatalf("代理域名不符: %q / %q", srcs[1].url, srcs[2].url)
	}
}

func TestNormalizeChangelog(t *testing.T) {
	// manifest 风格：<br> 分行 + 「更新日志」标题行，应转为纯文本换行并去标题
	in := "更新日志<br>v4.1.3.3:<br>1. transmission-daemon 改为 Alpine musl 全静态编译，去除 glibc 动态库依赖<br>2. WebUI 更换为基于 Go+React 的 SeedArk 管理面板"
	want := "v4.1.3.3:\n1. transmission-daemon 改为 Alpine musl 全静态编译，去除 glibc 动态库依赖\n2. WebUI 更换为基于 Go+React 的 SeedArk 管理面板"
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
