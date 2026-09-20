package torrentcreate

import (
	"bytes"
	"crypto/sha1"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// decodeBencode 测试用极简 bencode 解码（仅覆盖本包编码器产出的结构）
func decodeBencode(t *testing.T, data []byte) (any, int) {
	t.Helper()
	v, n, err := decodeValue(data)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	return v, n
}

func decodeValue(data []byte) (any, int, error) {
	if len(data) == 0 {
		return nil, 0, strconv.ErrSyntax
	}
	switch data[0] {
	case 'i':
		end := bytes.IndexByte(data, 'e')
		if end < 0 {
			return nil, 0, strconv.ErrSyntax
		}
		n, err := strconv.ParseInt(string(data[1:end]), 10, 64)
		return n, end + 1, err
	case 'l':
		out := []any{}
		pos := 1
		for pos < len(data) && data[pos] != 'e' {
			v, n, err := decodeValue(data[pos:])
			if err != nil {
				return nil, 0, err
			}
			out = append(out, v)
			pos += n
		}
		return out, pos + 1, nil
	case 'd':
		out := map[string]any{}
		pos := 1
		for pos < len(data) && data[pos] != 'e' {
			kv, n, err := decodeValue(data[pos:])
			if err != nil {
				return nil, 0, err
			}
			key, ok := kv.([]byte)
			if !ok {
				return nil, 0, strconv.ErrSyntax
			}
			vv, n2, err := decodeValue(data[pos+n:])
			if err != nil {
				return nil, 0, err
			}
			out[string(key)] = vv
			pos += n + n2
		}
		return out, pos + 1, nil
	default:
		colon := bytes.IndexByte(data, ':')
		if colon < 0 {
			return nil, 0, strconv.ErrSyntax
		}
		n, err := strconv.Atoi(string(data[:colon]))
		if err != nil || colon+1+n > len(data) {
			return nil, 0, strconv.ErrSyntax
		}
		end := colon + 1 + n
		return data[colon+1 : end], end, nil
	}
}

// TestBuildSingleFile 单文件建种：info 结构与分片哈希正确
func TestBuildSingleFile(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("hello seedark "), 4096) // 57344 字节
	src := filepath.Join(dir, "sample.bin")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	data, name, err := Build(src, Options{Announce: "http://tracker/announce", Private: true, PieceLength: 16384}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != "sample.bin" {
		t.Fatalf("种子名 = %q, 期望 sample.bin", name)
	}
	rootAny, _ := decodeBencode(t, data)
	root, ok := rootAny.(map[string]any)
	if !ok {
		t.Fatal("顶层不是字典")
	}
	info, ok := root["info"].(map[string]any)
	if !ok {
		t.Fatal("缺少 info 字典")
	}
	if got := string(info["name"].([]byte)); got != "sample.bin" {
		t.Fatalf("info.name = %q", got)
	}
	if got := info["length"].(int64); got != int64(len(content)) {
		t.Fatalf("info.length = %d, 期望 %d", got, len(content))
	}
	if got := info["private"].(int64); got != 1 {
		t.Fatalf("info.private = %v", got)
	}
	pieces := info["pieces"].([]byte)
	expectPieces := (len(content) + 16383) / 16384
	if len(pieces) != expectPieces*20 {
		t.Fatalf("pieces 长度 = %d, 期望 %d", len(pieces), expectPieces*20)
	}
	// 第一块与最后一块哈希手工验证
	if !bytes.Equal(pieces[:20], hashSum(content[:16384])) {
		t.Fatal("第一块哈希不匹配")
	}
	lastStart := (expectPieces - 1) * 16384
	if !bytes.Equal(pieces[(expectPieces-1)*20:], hashSum(content[lastStart:])) {
		t.Fatal("最后一块哈希不匹配")
	}
}

// TestBuildMultiFileCrossPiece 多文件目录建种：跨文件块边界规划正确
func TestBuildMultiFileCrossPiece(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	fa := bytes.Repeat([]byte{0xAA}, 20000)
	fb := bytes.Repeat([]byte{0xBB}, 20000)
	if err := os.WriteFile(filepath.Join(sub, "a.txt"), fa, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), fb, 0o644); err != nil {
		t.Fatal(err)
	}
	// 16KB 块：块 0 = a[0:16384]；块 1 = a[16384:20000] + b[0:12768]（跨文件）；块 2 = b[12768:20000]
	data, name, err := Build(sub, Options{PieceLength: 16384}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != "bundle" {
		t.Fatalf("种子名 = %q, 期望 bundle", name)
	}
	rootAny, _ := decodeBencode(t, data)
	root := rootAny.(map[string]any)
	info := root["info"].(map[string]any)
	files, ok := info["files"].([]any)
	if !ok || len(files) != 2 {
		t.Fatalf("info.files 缺失或数量不对: %v", info["files"])
	}
	f0 := files[0].(map[string]any)
	path0 := f0["path"].([]any)
	if string(path0[0].([]byte)) != "bundle" || string(path0[1].([]byte)) != "a.txt" {
		t.Fatalf("文件相对路径不对: %v", path0)
	}
	if f0["length"].(int64) != 20000 {
		t.Fatalf("a.txt 长度不对: %v", f0["length"])
	}
	pieces := info["pieces"].([]byte)
	if len(pieces) != 3*20 {
		t.Fatalf("pieces 长度 = %d, 期望 60", len(pieces))
	}
	// 块 0：a[0:16384]
	if !bytes.Equal(pieces[:20], hashSum(fa[:16384])) {
		t.Fatal("块 0 哈希不匹配")
	}
	// 块 1：跨文件 a[16384:20000] + b[0:12768]
	piece1 := append(append([]byte{}, fa[16384:]...), fb[:12768]...)
	if !bytes.Equal(pieces[20:40], hashSum(piece1)) {
		t.Fatal("跨文件块 1 哈希不匹配")
	}
	// 块 2：b[12768:20000]
	if !bytes.Equal(pieces[40:], hashSum(fb[12768:])) {
		t.Fatal("块 2 哈希不匹配")
	}
}

// TestBuildAnnounceList 多 tracker 分层与 url-list 编码
func TestBuildAnnounceList(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, _, err := Build(src, Options{
		Announce:     "http://a/announce",
		AnnounceList: [][]string{{"http://a/announce", "http://b/announce"}, {"http://c/announce"}},
		WebSeeds:     []string{"http://seed/1"},
		Comment:      "测试备注",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rootAny, _ := decodeBencode(t, data)
	root := rootAny.(map[string]any)
	if string(root["announce"].([]byte)) != "http://a/announce" {
		t.Fatalf("announce = %v", root["announce"])
	}
	tiers := root["announce-list"].([]any)
	if len(tiers) != 2 || len(tiers[0].([]any)) != 2 || len(tiers[1].([]any)) != 1 {
		t.Fatalf("announce-list 结构不对: %v", tiers)
	}
	if string(root["comment"].([]byte)) != "测试备注" {
		t.Fatalf("comment = %v", root["comment"])
	}
	seeds := root["url-list"].([]any)
	if len(seeds) != 1 || string(seeds[0].([]byte)) != "http://seed/1" {
		t.Fatalf("url-list = %v", seeds)
	}
}

// TestNormalizePieceLength 块大小归整
func TestNormalizePieceLength(t *testing.T) {
	cases := []struct {
		in, total, want int64
	}{
		{0, 100 << 20, 256 * 1024},  // 自动档（<256MB）
		{20 * 1024, 1, 32 * 1024},   // 20K 归整到 32K（向上取 2 的幂）
		{1 << 20, 1, 1 << 20},       // 合法值原样
		{64 << 20, 1, 16 << 20},     // 超上限夹取
		{100 * 1024, 1, 128 * 1024}, // 非 2 的幂向上归整
	}
	for _, c := range cases {
		if got := normalizePieceLength(c.in, c.total); got != c.want {
			t.Errorf("normalizePieceLength(%d, %d) = %d, 期望 %d", c.in, c.total, got, c.want)
		}
	}
}

func hashSum(b []byte) []byte {
	sum := sha1.Sum(b)
	return sum[:]
}
