# syntax=docker/dockerfile:1

# ---- 构建阶段：Go 1.22，测试随构建一并执行 ----
FROM golang:1.22-bookworm AS build
WORKDIR /src

# 先拉依赖，利用层缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并执行全部自动化测试（任一失败则镜像构建失败）
COPY . .
RUN CGO_ENABLED=0 go test ./... -count=1

# 静态编译，目标产物可跑在极简基础镜像上
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w" -o /out/stefan-server ./cmd/server
# 预置空数据目录（最终阶段用 COPY --chown 带属主拷入；放 .keep 占位
# 以避免空目录 COPY 在不同 BuildKit 版本下行为不一致）
RUN mkdir -p /out/data && touch /out/data/.keep

# ---- 运行阶段 ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /

# distroless 无 shell，不能 RUN mkdir/chown；用 COPY --chown 创建
# 归属 nonroot（uid/gid 65532）的 /data。Docker 会把挂载点目录的
# 属主/权限带进新卷，否则默认 root:0755 会导致非 root 进程无法写档。
COPY --chown=65532:65532 --from=build /out/data /data
COPY --from=build /out/stefan-server /usr/local/bin/stefan-server

# 容器内持久化工况档目录（可挂卷覆盖）
ENV STEFAN_DATA_DIR=/data \
    STEFAN_ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/stefan-server"]
