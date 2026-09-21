package main

import (
	"context"
	"fmt"
	"time"
)

type Click struct {
	ClickedAt time.Time
	UserAgent string
	Referer   string
	IP        string
}

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

func (s *Store) RecordClick(ctx context.Context, code string, c Click) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO clicks (link_id,user_agent,referer,ip)
		SELECT id,?,?,? FROM links WHERE code = ?
		`, c.UserAgent, c.Referer, c.IP, code)
	return err
}

type LinkStat struct {
	Code      string
	URL       string
	CreatedAt time.Time
	Clicks    int64
}

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
