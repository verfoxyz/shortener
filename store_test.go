package main

import (
	"context"
	"errors"
	"testing"
)

func TestStoreSaveAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Save(ctx, "abc123", "https://go.dev"); err != nil {
		t.Fatalf("save:%v", err)
	}

	got, err := s.Get(ctx, "abc123")
	if err != nil {
		t.Fatalf("get:%v", err)
	}
	if got != "https://go.dev" {
		t.Errorf("got %q,want %q", got, "https://go.dev")
	}
}

func TestStoreGetNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Get(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestStoreDuplicateCode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Save(ctx, "dup", "https://a.com"); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := s.Save(ctx, "dup", "https://b.com"); err == nil {
		t.Fatal("second save with same code should fail")
	}
}

func TestRecordClickAndListStats(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Save(ctx, "c1", "https://a.com"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Save(ctx, "c2", "https://b.com"); err != nil {
		t.Fatalf("save: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.RecordClick(ctx, "c1", Click{UserAgent: "test"}); err != nil {
			t.Fatalf("record click: %v", err)
		}
	}

	stats, err := s.LinkStat(ctx)
	if err != nil {
		t.Fatalf("list stats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d stats, want 2", len(stats))
	}

	byCode := map[string]int64{}
	for _, st := range stats {
		byCode[st.Code] = st.Clicks
	}
	if byCode["c1"] != 3 {
		t.Errorf("c1 clicks = %d, want 3", byCode["c1"])
	}
	if byCode["c2"] != 0 {
		t.Errorf("c2 clicks = %d, want 0", byCode["c2"])
	}
}
