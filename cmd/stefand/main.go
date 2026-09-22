// Command stefand 启动一维 Stefan 凝固锋面核算 HTTP 服务。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"stefan-service/internal/profile"
	"stefan-service/internal/server"
)

func main() {
	addr := ":" + envOr("STEFAN_PORT", "8080")
	dataDir := envOr("STEFAN_DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("创建数据目录 %s 失败：%v", dataDir, err)
	}

	store, err := profile.NewStore(filepath.Join(dataDir, "profiles.json"))
	if err != nil {
		log.Fatalf("打开工况档失败：%v", err)
	}
	if err := store.SeedDefaults(); err != nil {
		log.Fatalf("预置工况档写入失败：%v", err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(store).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("stefand 监听 %s（数据目录 %s）", addr, dataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务退出：%v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("收到退出信号，开始优雅关停")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("优雅关停失败：%v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
