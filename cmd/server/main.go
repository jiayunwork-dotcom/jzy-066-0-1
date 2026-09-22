// Command server 启动一维 Stefan 凝固锋面核算 HTTP 服务。
//
// 持久化目录由 STEFAN_DATA_DIR 指定（默认 /data），
// 监听地址由 STEFAN_ADDR 指定（默认 :8080）。
// 容器启动即对外提供接口，并预置冰层算例供核对。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"stefanservice/internal/httpx"
	"stefanservice/internal/job"
	"stefanservice/internal/profile"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dataDir := envOr("STEFAN_DATA_DIR", "/data")
	addr := envOr("STEFAN_ADDR", ":8080")

	store, err := profile.NewStore(dataDir)
	if err != nil {
		log.Fatalf("打开工况存储失败: %v", err)
	}
	if err := store.EnsureBuiltin(profile.IceProfile()); err != nil {
		log.Fatalf("预置冰层算例失败: %v", err)
	}

	jobs := job.NewManager()
	app := &httpx.App{Profiles: store, Jobs: jobs}
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpx.NewRouter(app),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("stefan-front 服务启动，监听 %s，工况目录 %s", addr, dataDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务退出: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("收到关停信号：取消在途时间序列作业并优雅关停 HTTP")
	jobs.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("HTTP 优雅关停超时: %v", err)
	}
	log.Println("已退出")
}
