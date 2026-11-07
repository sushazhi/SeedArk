package pt

import "testing"

// 同内容判定的单测。这一页最容易骗人：把「辅种共用一份数据」当成「冗余可省」，
// 所以三个分支必须各自钉死——尤其是 shared 一定要给出 0，绝不能给虚数。

func refs(entries ...[4]string) []TorrentRef {
	// 每项：name / serverName / kind / contentPath（体积固定 10GB，便于算差值）
	out := make([]TorrentRef, 0, len(entries))
	for i, e := range entries {
		out = append(out, TorrentRef{
			ID: int64(i + 1), Name: e[0], ServerName: e[1], Kind: e[2],
			ContentPath: e[3], Size: 10 << 30, Site: "site" + e[1],
		})
	}
	return out
}

func TestMatrixSharedWhenSamePath(t *testing.T) {
	// 同一份数据、两个站的种子指向同一路径 → 辅种，可省必须是 0
	m := matrixOf(refs(
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "qB", "qbittorrent", "/data/Movie.2024.1080p.WEB-DL-GRP"},
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "qB", "qbittorrent", "/data/Movie.2024.1080p.WEB-DL-GRP"},
	))
	if len(m.Groups) != 1 {
		t.Fatalf("组数 = %d, want 1", len(m.Groups))
	}
	g := m.Groups[0]
	if g.Verdict != VerdictShared {
		t.Errorf("verdict = %s, want shared", g.Verdict)
	}
	if g.ReclaimableBytes != 0 {
		t.Errorf("辅种组的可省体积必须是 0（删种子不释放空间），got %d", g.ReclaimableBytes)
	}
	if m.Summary.ReclaimableBytes != 0 || m.Summary.SharedGroups != 1 {
		t.Errorf("汇总 = (可省 %d, 辅种组 %d), want (0, 1)", m.Summary.ReclaimableBytes, m.Summary.SharedGroups)
	}
}

func TestMatrixDuplicateWithinServer(t *testing.T) {
	// 同一台下载器里两条不同路径 → 真重复：可省 = 组内体积 − 最大那份 = 10GB
	m := matrixOf(refs(
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "qB", "qbittorrent", "/data/a/Movie.2024.1080p.WEB-DL-GRP"},
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "qB", "qbittorrent", "/data/b/Movie.2024.1080p.WEB-DL-GRP"},
	))
	g := m.Groups[0]
	if g.Verdict != VerdictDuplicate {
		t.Fatalf("verdict = %s, want duplicate", g.Verdict)
	}
	if want := int64(10 << 30); g.ReclaimableBytes != want {
		t.Errorf("可省 = %d, want %d", g.ReclaimableBytes, want)
	}
	if !g.SameDownloader {
		t.Error("同一台下载器内两份应标记 SameDownloader")
	}
}

func TestMatrixUnknownAcrossServers(t *testing.T) {
	// 跨下载器、路径不同：两台机器路径体系不同，既不能证明共用也不能证明重复
	m := matrixOf(refs(
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "qB", "qbittorrent", "/data/Movie.2024.1080p.WEB-DL-GRP"},
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "TR", "transmission", "/volume1/video/Movie.2024.1080p.WEB-DL-GRP"},
	))
	g := m.Groups[0]
	if g.Verdict != VerdictUnknown {
		t.Errorf("verdict = %s, want unknown", g.Verdict)
	}
	if g.ReclaimableBytes != 0 {
		t.Errorf("不确定时不得给可省数字，got %d", g.ReclaimableBytes)
	}
	if !g.MultiServer {
		t.Error("跨下载器应标记 MultiServer")
	}
}

func TestMatrixSharedAcrossServersWithSamePath(t *testing.T) {
	// 两台下载器在同一台机器上指向同一路径（TR + qB 共用数据）→ 仍是辅种
	m := matrixOf(refs(
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "qB", "qbittorrent", "/data/Movie.2024.1080p.WEB-DL-GRP"},
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "TR", "transmission", "/data/Movie.2024.1080p.WEB-DL-GRP"},
	))
	if g := m.Groups[0]; g.Verdict != VerdictShared {
		t.Errorf("路径相同应判共用，got %s", g.Verdict)
	}
}

func TestMatrixGroupsByNormalizedName(t *testing.T) {
	// 归一化：分隔符差异应合并；分辨率不同不得合并
	m := matrixOf(refs(
		[4]string{"Movie.2024.1080p.WEB-DL-GRP", "qB", "qbittorrent", "/data/x"},
		[4]string{"Movie_2024_1080p_WEB-DL_GRP", "TR", "transmission", "/data/y"},
		[4]string{"Movie.2024.2160p.WEB-DL-GRP", "qB", "qbittorrent", "/data/z"},
		[4]string{"Solo.Release.2019.1080p", "qB", "qbittorrent", "/data/solo"},
	))
	if len(m.Groups) != 1 {
		t.Fatalf("组数 = %d, want 1（1080p 的两种写法合并，2160p 与独苗不成组）", len(m.Groups))
	}
	if got := len(m.Groups[0].Copies); got != 2 {
		t.Errorf("副本数 = %d, want 2", got)
	}
}
