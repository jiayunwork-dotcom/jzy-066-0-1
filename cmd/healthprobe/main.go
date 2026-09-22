// Command healthprobe 是容器 HEALTHCHECK 用的最小探针：
// GET 本机 /healthz，200 返回退出码 0，其余情况退出 1。
// 单独成二进制，使运行镜像可选用没有 shell/wget 的 distroless 基础镜像。
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("STEFAN_PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "health check failed:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy status:", resp.StatusCode)
		os.Exit(1)
	}
}
