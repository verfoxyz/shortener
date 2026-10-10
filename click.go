package main

import (
	"context"
	"fmt"
	"time"
)

// Click 是一次点击的数据形状。
// 它只用于"写入"方向 —— 读取统计走的是 LinkStat 聚合，不会把 Click 读回来，
// 所以字段上不需要任何 db tag / Scan 顺序之类的映射信息。
// ClickedAt 由 SQL 的 CURRENT_TIMESTAMP 填充，Go 侧不赋值，故留空。
type Click struct {
	ClickedAt time.Time
	UserAgent string
	Referer   string
	IP        string
}

// migrateClick 负责 clicks 表及其索引。
// 单独拆一个方法（而不是并进 migrate）是因为它属于"点击统计"这块功能，
// 跟着 click.go 一起放，读的人才知道 clicks 表的定义在这个文件里。
//
// ⚠️ 注意：SQLite 默认不启用外键约束，下面这条 FOREIGN KEY 目前只是声明，
// 想真正生效需要开 PRAGMA foreign_keys=ON（或在 DSN 上加 _pragma=foreign_keys(1)）。
func (s *Store) migrateClick() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS clicks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			link_id INTEGER NOT NULL,
			clicked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			user_agent TEXT,
			referer TEXT,
			ip TEXT,
			FOREIGN KEY (link_id) REFERENCES links(id)
		);
		CREATE INDEX IF NOT EXISTS idx_clicks_link_id ON clicks(link_id);
	`)
	if err != nil {
		return fmt.Errorf("migrate clicks:%w", err)
	}
	return nil
}

// RecordClick 记录一次点击。
//
// 这里用 INSERT ... SELECT 而不是"先 SELECT id 再 INSERT"两步：
// 一条语句内就完成 code → link_id 的换算，省掉一次往返，也不必手写事务。
// 若 code 不存在，SELECT 返回 0 行，INSERT 自然什么都不插（不报错，属静默无操作）。
func (s *Store) RecordClick(ctx context.Context, code string, c Click) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO clicks (link_id,user_agent,referer,ip)
		SELECT id,?,?,? FROM links WHERE code = ?
		`, c.UserAgent, c.Referer, c.IP, code)
	return err
}

// LinkStat 是看板一行所需的数据形状：短链元信息 + 点击数。
type LinkStat struct {
	Code      string
	URL       string
	CreatedAt time.Time
	Clicks    int64
}

// LinkStat 查询所有短链及其点击次数。
//
// 用 LEFT JOIN 而非 JOIN：一次点击都没有的链接点击数是 0，也必须出现在看板里。
// GROUP BY l.id（主键）而不是 GROUP BY l.code，按"链接"分组语义更准确。
// defer rows.Close() 与末尾的 rows.Err() 是 database/sql 的标准收尾两件套：
// 前者释放连接，后者捕获"遍历过程中"才暴露的错误（Scan 的 err 只覆盖单行）。
func (s *Store) LinkStat(ctx context.Context) ([]LinkStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.code, l.url, l.created_at, COUNT(c.id) AS clicks
		FROM links l
		LEFT JOIN clicks c ON c.link_id = l.id
		GROUP BY l.id
		ORDER BY l.created_at DESC
		`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []LinkStat
	for rows.Next() {
		var s LinkStat
		if err := rows.Scan(&s.Code, &s.URL, &s.CreatedAt, &s.Clicks); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}
