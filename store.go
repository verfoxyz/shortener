package main

// 本文件：Store 的建立、连接参数、迁移入口，以及 links 表的读写。
// 注意 click.go 里的方法也挂在 *Store 上 —— Go 同一 package 的多个文件是"合并"看待的，
// 所以 Store 的方法分散在两个文件里，读的时候要把它们当成同一个类型。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	// 匿名导入（_）：只为触发驱动的 init() 完成注册。
	// 代码里不会直接引用这个包，所以必须用 _ 而不是给它取别名。
	_ "modernc.org/sqlite"
)

// ErrNotFound 是哨兵错误（sentinel error），代表"短码不存在"这一种可预期的业务结果。
// 调用方用 errors.Is 判断它，从而把"没找到"（回 404）与"数据库出错"（回 500）区分开。
// 这是本文件对上层最重要的一个契约。
var ErrNotFound = errors.New("link not found")

// Store 封装全部数据访问逻辑。对外暴露方法而非 *sql.DB，
// 让 handler 不依赖 SQL 细节，测试时也能整体替换实现。
type Store struct {
	db *sql.DB
}

// NewStore 打开（必要时创建）SQLite 库并完成建表。
func NewStore(dbPath string) (*Store, error) {
	// DSN 追加两个 pragma：
	//   busy_timeout(5000) → 写锁被占用时忙等重试最多 5 秒，而不是立刻报 "database is locked"；
	//   journal_mode(WAL)  → WAL 模式下读不阻塞写，显著改善并发读性能。
	// 因为是字符串拼接，DBPath 若自带 "?" 会破坏 DSN 结构（见 Config 的说明）。
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"

	// sql.Open 是惰性的：只校验参数、不真正建立连接，所以这里的 err 基本只代表 DSN 格式错误。
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db:%w", err)
	}
	// 用 Ping 主动探一次连接，把"库文件不可写"之类的问题在启动期就暴露出来（fail fast）。
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db:%w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		// 初始化失败必须关掉已打开的连接再返回，否则连接泄漏。
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// migrate 建 links 表，然后串到 clicks 表的迁移。
// 用 CREATE TABLE IF NOT EXISTS 做"幂等迁移"：每次启动都跑一遍，已存在则什么都不做，
// 因此无需引入任何迁移框架。
func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT UNIQUE NOT NULL,
			url TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return fmt.Errorf("migrate:%w", err)
	}
	return s.migrateClick()
}

// Close 释放连接池，由 main 在 HTTP 服务停止之后调用。
func (s *Store) Close() error {
	return s.db.Close()
}

// Save 写入一条"短码 → 原链接"映射。
// code 上的 UNIQUE 约束是短码去重的唯一保障：撞码时这里返回错误，
// 由 Handler.Create 负责换个短码重试（所以本方法自己不重试）。
func (s *Store) Save(ctx context.Context, code, url string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO links (code,url)VALUES(?,?)`, code, url)
	return err
}

// Get 按短码查原链接。
// 把 sql.ErrNoRows 翻译成 ErrNotFound，相当于把 database/sql 的细节挡在包内，
// 让上层只面对"业务语言"的错误。
func (s *Store) Get(ctx context.Context, code string) (string, error) {
	var url string
	err := s.db.QueryRowContext(ctx,
		`SELECT url FROM links WHERE code = ?`, code).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return url, nil
}
