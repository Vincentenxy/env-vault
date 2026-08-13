# ---- 构建阶段 ----
FROM golang:1.26-alpine AS builder

WORKDIR /build

# 优先拷贝依赖文件，利用镜像层缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server ./cmd

# ---- 运行阶段 ----
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/server .
# 默认配置文件（K8s 部署时可通过 ConfigMap 挂载覆盖）
COPY --from=builder /build/configs ./configs

EXPOSE 8080

ENTRYPOINT ["/app/server"]
