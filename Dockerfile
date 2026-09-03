# WatchDog AI —— Docker 镜像（多阶段构建）
#
# 用法：
#   docker build -t watchdog-ai .
#   docker run -d --name watchdog -p 9191:9191 -v watchdog-data:/opt/watchdog/data watchdog-ai
#
# 说明：镜像内置 SQLite（纯 Go 驱动，无 CGO），开箱即用；数据目录通过卷持久化。

# ---- 阶段一：构建前端 ----
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- 阶段二：编译后端 ----
FROM golang:1.22-alpine AS build
WORKDIR /src
# 使用纯 Go SQLite（glebarez/sqlite），可静态编译，无需 CGO
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN go build -ldflags="-s -w" -o /watchdog ./cmd/watchdog

# ---- 阶段三：运行 ----
FROM alpine:3.19
RUN apk add --no-cache ca-certificates \
  && adduser -D -H -u 1000 watchdog
WORKDIR /app
COPY --from=build /watchdog /app/watchdog
COPY --from=build /src/web/dist /app/web/dist
RUN mkdir -p /app/data && chown -R watchdog:watchdog /app
USER watchdog
EXPOSE 9191
ENV WATCHDOG_BIND=:9191 \
    WATCHDOG_DATA_DIR=/app/data
VOLUME ["/app/data"]
ENTRYPOINT ["/app/watchdog"]