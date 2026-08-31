package session

import (
	"context"
	"testing"
	"time"
)

func newSess(id, sub string, created time.Time) *Session {
	return &Session{ID: id, Sub: sub, Owner: "lens", Name: "alice", Roles: []string{"customer"}, CreatedAt: created}
}

func TestMemoryCRUD(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	s := NewMemoryStore(DefaultTTL())
	s.now = func() time.Time { return now }

	if err := s.Create(ctx, newSess("a", "u1", now)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "a")
	if err != nil || got.Sub != "u1" || len(got.Roles) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}

	// 删除即撤权：立刻不可见。
	if err := s.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "a"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestIdleExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	s := NewMemoryStore(TTL{Idle: time.Hour, Absolute: 24 * time.Hour})
	s.now = func() time.Time { return now }
	_ = s.Create(ctx, newSess("a", "u1", now))

	// 30 分钟后访问：仍在，且续期空闲窗。
	now = now.Add(30 * time.Minute)
	if _, err := s.Get(ctx, "a"); err != nil {
		t.Fatalf("should still be alive: %v", err)
	}
	// 再过 50 分钟（距上次访问 50min < 60min 空闲窗）：仍在——证明访问确实续期了。
	now = now.Add(50 * time.Minute)
	if _, err := s.Get(ctx, "a"); err != nil {
		t.Fatalf("idle window should have been extended by the last access: %v", err)
	}
	// 静置超过空闲窗：失效。
	now = now.Add(2 * time.Hour)
	if _, err := s.Get(ctx, "a"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after idle timeout, got %v", err)
	}
}

// 绝对上限不受活跃度影响——这是「长期挂着的会话也必须重新登录」的保证。
func TestAbsoluteExpiry(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	now := start
	s := NewMemoryStore(TTL{Idle: time.Hour, Absolute: 3 * time.Hour})
	s.now = func() time.Time { return now }
	_ = s.Create(ctx, newSess("a", "u1", start))

	// 每 30 分钟访问一次（空闲窗永远续上），但越过绝对上限后必须失效。
	for i := 0; i < 5; i++ {
		now = now.Add(30 * time.Minute)
		_, _ = s.Get(ctx, "a")
	}
	now = start.Add(3*time.Hour + time.Minute)
	if _, err := s.Get(ctx, "a"); err != ErrNotFound {
		t.Fatalf("absolute cap must win over activity, got %v", err)
	}
}

// 会话清单与整户踢出——这是选 A 方案（服务端 session）才有的能力。
func TestListAndDeleteByUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	s := NewMemoryStore(DefaultTTL())
	s.now = func() time.Time { return now }
	_ = s.Create(ctx, newSess("a", "u1", now))
	_ = s.Create(ctx, newSess("b", "u1", now))
	_ = s.Create(ctx, newSess("c", "u2", now))

	list, err := s.ListByUser(ctx, "u1")
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
	n, err := s.DeleteByUser(ctx, "u1")
	if err != nil || n != 2 {
		t.Fatalf("deleted=%d err=%v", n, err)
	}
	if _, err := s.Get(ctx, "c"); err != nil {
		t.Fatal("other users must be untouched")
	}
}

func TestNewIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) < 40 || seen[id] {
			t.Fatalf("weak or duplicate id: %q", id)
		}
		seen[id] = true
	}
}
