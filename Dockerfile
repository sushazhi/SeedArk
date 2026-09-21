# syntax=docker/dockerfile:1
# 多阶段构建：前端（Node + pnpm）→ 后端（Go，内嵌前端产物）→ 最小运行镜像
# 产物只有一个静态二进制（CGO_ENABLED=0）+ 内嵌前端，支持 linux/amd64、linux/arm64。
#
# 构建：docker build -t seedark .
# 运行：docker run -d -p 8200:8200 -e API_TOKEN=xxx -e SA_URL=http://host:9091/transmission/rpc seedark

# ---------- 1. 构建前端 ----------
FROM node:24-alpine AS web
WORKDIR /src

# pnpm 12（与 CI、开发机一致，pnpm-workspace.yaml 的 allowBuilds 需要 11+）
RUN corepack enable && corepack prepare pnpm@12 --activate

# 先只复制清单文件，依赖未变动时可命中缓存层
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

COPY frontend/ ./
RUN pnpm build

# ---------- 2. 编译后端（go:embed 读取 backend/web/dist） ----------
FROM golang:1.27-alpine AS server
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
# 覆盖为本次构建的前端产物（仓库里的 web/dist 是 gitignore 的本地产物，构建上下文已排除）
RUN rm -rf web/dist
COPY --from=web /src/dist/ web/dist/

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w" -o /out/seedark ./cmd/server

# ---------- 3. 运行镜像 ----------
FROM alpine:3.24
LABEL org.opencontainers.image.title="seedark" \
      org.opencontainers.image.description="现代化 Transmission Web 管理面板（Go + React，专为 fnOS 设计）" \
      org.opencontainers.image.source="https://github.com/sushazhi/seedark" \
      org.opencontainers.image.licenses="MIT"

# ca-certificates：访问 https 形式的 RPC / GitHub Release 需要
# tzdata：允许通过 -e TZ=Asia/Shanghai 得到本地时区日志
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app \
    && mkdir -p /data && chown -R app:app /data

COPY --from=server /out/seedark /usr/local/bin/seedark

# 容器内必须监听 0.0.0.0；非回环地址 + 未设置 API_TOKEN 时服务会拒绝启动并给出提示
ENV SERVER_HOST=0.0.0.0 \
    SERVER_PORT=8200 \
    SA_DATA_DIR=/data

USER app
WORKDIR /app
VOLUME ["/data"]
EXPOSE 8200

# 首页属于静态资源，不受 API_TOKEN 鉴权影响，可直接作为健康检查端点
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8200/ || exit 1

ENTRYPOINT ["/usr/local/bin/seedark"]
