# syntax=docker/dockerfile:1

# ---- 构建阶段：拉依赖、跑全部自动化测试、产出静态二进制 ----
FROM golang:1.22-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 自动化测试随镜像构建一并执行：超越方程求根收敛、√t 推进关系、
# 小 Ste 近似的适用与失效、非法物性拦截与并发隔离任一不过，构建直接失败。
RUN go test ./...

RUN mkdir -p /out/data
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/stefand ./cmd/stefand
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/healthprobe ./cmd/healthprobe

# ---- 运行阶段：最小 distroless，无 shell、无网页界面 ----
FROM gcr.io/distroless/static-debian12:nonroot

# /data 在构建期建好并归非 root 用户，供工况档 JSON 持久化与卷挂载。
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/stefand     /usr/bin/stefand
COPY --from=build --chown=65532:65532 /out/healthprobe /usr/bin/healthprobe

ENV STEFAN_PORT=8080 \
    STEFAN_DATA_DIR=/data

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=10s --timeout=3s --start-period=3s --retries=3 \
    CMD ["/usr/bin/healthprobe"]

ENTRYPOINT ["/usr/bin/stefand"]
