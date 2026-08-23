package resolver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeLister 用 channel 驱动 List 的阻塞式返回。
type fakeLister struct {
	updates chan []Instance
	errs    chan error
}

func newFakeLister() *fakeLister {
	return &fakeLister{updates: make(chan []Instance, 8), errs: make(chan error, 8)}
}

func (f *fakeLister) List(ctx context.Context, _ string, index uint64) ([]Instance, uint64, error) {
	select {
	case list := <-f.updates:
		return list, index + 1, nil
	case err := <-f.errs:
		return nil, index, err
	case <-ctx.Done():
		return nil, index, ctx.Err()
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestWatchPickReady(t *testing.T) {
	f := newFakeLister()
	w := NewWatching(f, []string{"user-identity"})
	defer w.Close()

	if w.Ready() {
		t.Fatal("must not be ready before first snapshot")
	}
	f.updates <- []Instance{{Addr: "10.0.0.1:8080"}, {Addr: "10.0.0.2:8080"}}
	waitUntil(t, w.Ready)

	inst, done, err := w.Pick("user-identity")
	if err != nil {
		t.Fatal(err)
	}
	if inst.Addr == "" {
		t.Fatal("empty instance")
	}
	done(nil)
}

func TestUnknownService(t *testing.T) {
	w := NewWatching(newFakeLister(), []string{"a"})
	defer w.Close()
	if _, _, err := w.Pick("nope"); !errors.Is(err, ErrUnknownSvc) {
		t.Fatalf("want ErrUnknownSvc, got %v", err)
	}
}

// Consul ACL 静默失败：空结果保留上一份快照并触发告警回调。
func TestEmptyResultKeepsLastKnownGood(t *testing.T) {
	f := newFakeLister()
	var alerts atomic.Int64
	w := NewWatching(f, []string{"svc"})
	defer w.Close()
	w.OnEmptyResult = func(service string, streak int64) { alerts.Add(1) }

	f.updates <- []Instance{{Addr: "10.0.0.1:1"}}
	waitUntil(t, w.Ready)
	f.updates <- []Instance{} // 空结果
	waitUntil(t, func() bool { return alerts.Load() >= 1 })

	if _, done, err := w.Pick("svc"); err != nil {
		t.Fatalf("must keep last-known-good: %v", err)
	} else {
		done(nil)
	}
}

// 快照替换不得清零实例状态（旧网关健康检查被刷新清零的历史坑）。
func TestSwapPreservesNodeState(t *testing.T) {
	f := newFakeLister()
	w := NewWatching(f, []string{"svc"})
	defer w.Close()

	f.updates <- []Instance{{Addr: "10.0.0.1:1"}, {Addr: "10.0.0.2:1"}}
	waitUntil(t, w.Ready)

	// 给 .1 制造两次失败（未达冷却阈值）。
	failTwice(t, w, "10.0.0.1:1")

	// 刷新快照（同实例集合）。
	f.updates <- []Instance{{Addr: "10.0.0.1:1"}, {Addr: "10.0.0.2:1"}}
	time.Sleep(50 * time.Millisecond)

	// 第三次失败必须触发冷却——若状态被刷新清零则不会触发。
	failOn(t, w, "10.0.0.1:1")
	st := w.services["svc"]
	st.mu.RLock()
	n := st.nodes["10.0.0.1:1"]
	st.mu.RUnlock()
	if n.healthy(time.Now()) {
		t.Fatal("third consecutive failure must put node into cooldown; state was reset by swap")
	}
}

func failTwice(t *testing.T, w *Watching, addr string) {
	t.Helper()
	for i := 0; i < 2; i++ {
		failOn(t, w, addr)
	}
}

// failOn 反复 Pick 直到命中目标实例并报告失败。
func failOn(t *testing.T, w *Watching, addr string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		inst, done, err := w.Pick("svc")
		if err != nil {
			t.Fatal(err)
		}
		if inst.Addr == addr {
			done(errors.New("boom"))
			return
		}
		done(nil)
	}
	t.Fatalf("never picked %s", addr)
}

func TestCooldownFiltersAndFallback(t *testing.T) {
	f := newFakeLister()
	w := NewWatching(f, []string{"svc"})
	defer w.Close()

	f.updates <- []Instance{{Addr: "bad:1"}, {Addr: "good:1"}}
	waitUntil(t, w.Ready)

	for i := 0; i < 3; i++ {
		failOn(t, w, "bad:1")
	}
	// 冷却期内不应再选中 bad。
	for i := 0; i < 50; i++ {
		inst, done, err := w.Pick("svc")
		if err != nil {
			t.Fatal(err)
		}
		done(nil)
		if inst.Addr == "bad:1" {
			t.Fatal("cooled-down node must be filtered")
		}
	}

	// 唯一实例全冷却时退化为可选（宁可打冷却节点也不报无实例）。
	f2 := newFakeLister()
	w2 := NewWatching(f2, []string{"svc"})
	defer w2.Close()
	f2.updates <- []Instance{{Addr: "only:1"}}
	waitUntil(t, w2.Ready)
	for i := 0; i < 3; i++ {
		failOn(t, w2, "only:1")
	}
	if _, done, err := w2.Pick("svc"); err != nil {
		t.Fatalf("all-down fallback must still pick: %v", err)
	} else {
		done(nil)
	}
}

func TestDoneExactlyOnce(t *testing.T) {
	f := newFakeLister()
	w := NewWatching(f, []string{"svc"})
	defer w.Close()
	f.updates <- []Instance{{Addr: "a:1"}}
	waitUntil(t, w.Ready)

	_, done, err := w.Pick("svc")
	if err != nil {
		t.Fatal(err)
	}
	done(nil)
	done(errors.New("second call ignored"))

	st := w.services["svc"]
	st.mu.RLock()
	n := st.nodes["a:1"]
	st.mu.RUnlock()
	if got := n.inflight.Load(); got != 0 {
		t.Fatalf("inflight=%d want 0 (done must be exactly-once)", got)
	}
}

func TestP2CPrefersLowerInflight(t *testing.T) {
	f := newFakeLister()
	w := NewWatching(f, []string{"svc"})
	defer w.Close()
	f.updates <- []Instance{{Addr: "busy:1"}, {Addr: "idle:1"}}
	waitUntil(t, w.Ready)

	// 人为压高 busy 的在途数。
	st := w.services["svc"]
	st.mu.RLock()
	st.nodes["busy:1"].inflight.Store(100)
	st.mu.RUnlock()

	idle := 0
	for i := 0; i < 100; i++ {
		inst, done, err := w.Pick("svc")
		if err != nil {
			t.Fatal(err)
		}
		if inst.Addr == "idle:1" {
			idle++
		}
		done(nil)
	}
	if idle < 95 {
		t.Fatalf("P2C should overwhelmingly prefer idle node, got %d/100", idle)
	}
}
