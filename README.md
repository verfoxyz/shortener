# shortener
个人学习Go时开发的项目。

一个用 Go 标准库写的短链接服务：生成短码、302 跳转、记录点击并在管理页看统计。单二进制、零外部依赖（SQLite 内嵌），可本地直接跑，也可用 Docker 部署。

## 功能

| 功能 | 接口 | 说明 |
| --- | --- | --- |
| 生成短链 | `POST /shorten` | 传入原始 URL，返回 6 位 base62 短码；短码冲突时自动重试（最多 5 次） |
| 跳转 | `GET /{code}` | 302 临时重定向，同时在跳转前记录一次点击（UA / Referer / IP） |
| 管理看板 | `GET /admin` | HTML 页面，列出全部短链、原链接、点击数、创建时间 |
| 健康检查 | `GET /healthz` | 返回 `ok` |

> 特意用 **302 而不是 301**：301 会被浏览器与中间层缓存，后续点击不再回到服务端，点击统计会直接失效。

## 技术栈

- **Go 1.27 标准库**：`net/http`（`ServeMux` 方法级路由 + `{code}` 通配符 + `PathValue`）、`html/template`、`database/sql`
- **SQLite**：`modernc.org/sqlite`（纯 Go 实现，`CGO_ENABLED=0` 也能编译）、WAL 模式
- **日志**：`log/slog` 输出 JSON 结构化日志
- **测试**：`httptest` + `t.TempDir()`（每个用例独立临时库，不污染开发数据）

## 快速开始

```bash
go run .            # 直接运行，默认监听 :8080
# 或
go build -o shortener . && ./shortener
```

### 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ADDR` | `:8080` | 监听地址 |
| `DB_PATH` | `shortener.db` | SQLite 文件路径 |
| `BASE_URL` | `http://localhost:8080` | 拼接返回的短链前缀 |

## 使用示例

```bash
# 创建短链
curl -X POST localhost:8080/shorten \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://go.dev"}'
# 201 {"code":"FAybNJ","short_url":"http://localhost:8080/FAybNJ"}

# 跳转（302，并记录一次点击）
curl -i localhost:8080/FAybNJ

# 管理看板：浏览器访问 http://localhost:8080/admin
```

错误响应：请求体不是合法 JSON / 缺 `url` / 非 `http(s)` 协议 → `400`；短码重试用尽或写库失败 → `500`；短码不存在 → `404`。

## 数据存储

```sql
CREATE TABLE links (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT UNIQUE NOT NULL,          -- 唯一索引，短码查重与查询都走它
    url        TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE clicks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    link_id    INTEGER NOT NULL,
    clicked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    user_agent TEXT,
    referer    TEXT,
    ip         TEXT,
    FOREIGN KEY (link_id) REFERENCES links(id)
);
CREATE INDEX idx_clicks_link_id ON clicks(link_id);
```

连接串会带上 `_pragma=busy_timeout(5000)` 与 `_pragma=journal_mode(WAL)`：WAL 让读不阻塞写，`busy_timeout` 让并发写忙等重试而不是立刻报错。

## 测试与静态检查

```bash
go test ./... -count=1          # 9 个用例：Create/Redirect/Admin/Store/点击统计
go test -cover ./... -count=1   # 当前语句覆盖率约 36.5%
go vet ./...
golangci-lint run ./...         # 配置见 .golangci.yml（errcheck / wrapcheck / staticcheck / gocritic 等）
```

## 容器化部署

```bash
docker build -t shortener .

docker run -d --name shortener -p 8080:8080 \
  -v shortener-data:/data \
  -e BASE_URL=http://localhost:8080 \
  shortener
```

镜像分两阶段构建：`golang:1.27.1` 里静态编译（`-trimpath -ldflags="-s -w"`），运行阶段用 `distroless/static`，数据库默认落在 `/data/shortener.db`（已声明 `VOLUME`）。

## 目录结构

```
shortener/
├── main.go            # 路由注册、三个 handler、优雅退出
├── store.go           # 连接与迁移、links 表的读写
├── click.go           # clicks 表迁移、点击写入、按链接聚合统计
├── middleware.go      # 访问日志中间件、panic 恢复中间件
├── config.go          # 环境变量配置
├── handler_test.go    # HTTP 层用例（创建/跳转/校验/看板）
├── store_test.go      # 存储层用例（读写/冲突/点击统计）
├── testutil_test.go   # 测试辅助：每个用例一个临时库
├── web/admin.html     # 管理看板模板
├── Dockerfile         # 多阶段构建（builder → distroless）
└── .golangci.yml      # 静态检查配置
```
