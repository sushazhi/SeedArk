package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sushazhi/seedark/backend/internal/qbmock"
)

func main() {
	var (
		addr   = flag.String("addr", "127.0.0.1:8080", "监听地址")
		user   = flag.String("user", "", "登录用户名（空 = 不校验）")
		pass   = flag.String("pass", "", "登录密码（空 = 不校验）")
		apiKey = flag.String("api-key", "", "API Key（qbt_ 开头共 32 位；空 = 自动生成）")
		compat = flag.String("compat", "5.x", "兼容模式：5.x（qBittorrent 5.2.3）或 4.x（隐藏 start/stop/setTags 验证回退）")
		seed   = flag.Int("seed", 14, "初始种子数量")
		tick   = flag.Duration("tick", 1*time.Second, "模拟推进间隔")
		static = flag.Bool("static", false, "关闭实时模拟（静态快照）")
	)
	flag.Parse()

	opts := qbmock.Options{
		User:   *user,
		Pass:   *pass,
		APIKey: *apiKey,
		Compat: strings.TrimSpace(*compat),
		Seed:   *seed,
	}
	srv := qbmock.New(opts)

	if !*static {
		go func() {
			t := time.NewTicker(*tick)
			defer t.Stop()
			for range t.C {
				srv.Step(*tick)
			}
		}()
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("qbmock 已启动: http://%s/api/v2/ （qBittorrent %s / WebAPI %s，种子 %d 个，鉴权 %v，API Key %v）",
			*addr, srv.Version(), srv.WebAPIVersion(), *seed, onOff(*user != "" || *pass != ""), onOff(srv.APIKey() != ""))
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("正在关闭 qbmock ...")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

func onOff(b bool) string {
	if b {
		return "开"
	}
	return "关"
}
