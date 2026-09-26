package state

import (
	"os"
	"path/filepath"
	"testing"
)

// 状态文件损坏不能让应用起不来：原文件改名留档，进程以默认状态继续
func TestLoadCorruptFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sa-state.json")
	if err := os.WriteFile(path, []byte("{ 这不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load 不应因文件损坏返回错误: %v", err)
	}
	if got := s.Get(); len(got.Servers) != 0 || len(got.SeedPolicyRules) != 0 {
		t.Fatalf("应以默认状态启动，实际 servers=%d rules=%d", len(got.Servers), len(got.SeedPolicyRules))
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("损坏文件应改名留档: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("应写回一份新的默认状态文件: %v", err)
	}
}

// 上一次的留档不得被覆盖：再损坏时另存带时间戳的新档
func TestLoadCorruptFileKeepsPreviousBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sa-state.json")
	previous := filepath.Join(dir, "sa-state.json.corrupt")
	if err := os.WriteFile(previous, []byte("上一轮损坏的内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("Load 不应返回错误: %v", err)
	}
	data, err := os.ReadFile(previous)
	if err != nil || string(data) != "上一轮损坏的内容" {
		t.Fatalf("旧留档被覆盖: err=%v content=%q", err, data)
	}
	matches, err := filepath.Glob(path + ".corrupt.*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("应另存一份带时间戳的留档: matches=%v err=%v", matches, err)
	}
}
