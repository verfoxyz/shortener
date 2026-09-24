# ---- 构建阶段 ----
FROM golang:1.27.1 AS builder

WORKDIR /app

# 先只拷贝依赖文件，利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

# 再拷贝源码
COPY . .

# 静态编译，关闭 CGO，方便在 alpine/distroless 跑
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/shortener .

# ---- 运行阶段 ----
FROM gcr.io/distroless/static-debian12

WORKDIR /app

# 拷贝二进制和模板
COPY --from=builder /out/shortener /app/shortener
COPY --from=builder /app/web /app/web

# SQLite 数据库放这个目录，方便挂卷
ENV DB_PATH=/data/shortener.db
VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/app/shortener"]