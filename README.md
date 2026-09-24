# shortener

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

## 性能与已知限制

在本机（Windows，压测客户端与服务同机）用固定并发 + keep-alive 压测的结果：

| 场景 | QPS | P50 | P99 | Max |
| --- | --- | --- | --- | --- |
| 写路径（`POST /shorten`）并发 1 | ~575 | 1.7ms | 3.3ms | 5.8ms |
| 读路径（`GET /{code}`）并发 4 | ~537 | 2.0ms | 108ms | 1.7s |
| 读路径并发 64 | ~512 | 4.0ms | **2.6s** | **5.06s** |

结论与原因：

- **吞吐天花板约 575 次/秒，且不随并发提升** —— 因为每次跳转都要同步 `INSERT` 一条点击记录，且每条 `INSERT` 是一个独立事务（WAL 下默认 `synchronous=FULL`，每次提交都 fsync），写被串行化。
- **Max 5.06s ≈ `busy_timeout(5000)`** —— 并发上来后请求是在写锁上排队重试到超时，属于排队而非快速失败。
- 改造方向：点击写入改 `channel` + 单写者批量事务；`synchronous` 在 WAL 下调到 `NORMAL`。

其他已知限制：

- 静态检查用的是偏严的配置（`.golangci.yml` 开了 `errcheck(check-blank)` / `wrapcheck` / `noctx` / `gocritic` / `gofumpt` 等），目前 `golangci-lint run ./...` 仍有约 20 项未处理，集中在：`noctx`（`Exec`/`Ping`/`httptest.NewRequest` 应换成 `...Context` 版本）、`wrapcheck`（外部包错误未包装）、`errcheck`（`rows.Close` 等返回值未检查）、测试文件格式（`gofumpt`），尚未逐条收敛。
- SQLite 的外键**默认不启用**，`clicks` 里的 `FOREIGN KEY` 目前只是声明，需要 `PRAGMA foreign_keys=ON`（或用 DSN `_pragma=foreign_keys(1)`）才真正生效。
- 短码用 `math/rand` 生成，**不是密码学安全**的；若短链指向私有资源，应换 `crypto/rand`。
- 单机 SQLite，写并发能力有限，也不能水平扩展；要扩容需换 MySQL/Postgres 并把短码映射放进 Redis。
- `srv.Shutdown` 只等 HTTP 请求结束，不负责回收自定义后台 goroutine（引入异步写点击后需要一并收尾）。
