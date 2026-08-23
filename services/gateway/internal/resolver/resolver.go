// Package resolver 实现后端实例解析与选点（Resolver seam，终裁 §四）。
//
// 契约：
//   - Pick 返回实例与 done 反馈闭包；done 必须且只能调用一次（exactly-once），
//     用于释放在途计数与被动健康反馈；
//   - 实例快照来自 Consul 服务目录 Watch（blocking query，只取 passing）；
//     快照替换是增量语义：仍存在的实例保留其健康/在途状态，不会被刷新清零（历史坑：
//     旧网关全删全加把 failureCount 清零，健康过滤长期形同虚设）；
//   - 选点为 P2C：随机取两个候选比在途数，另叠加被动冷却（连续失败进入冷却期）；
//   - Consul 空结果不视为「服务消失」而直接清表——ACL 静默失败会返回 200 空列表，
//     这里保留上一份非空快照并记告警计数（last-known-good）。
//
// K8s Service DNS 形态后续以另一个 Lister 实现接入，网关侧只换构造。
package resolver

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// Instance 是一个可转发的后端实例。
type Instance struct {
	// Addr 形如 host:port。
	Addr string
	// Weight 来自 Consul 实例 metadata（保留旧语义；0 视为 1）。
	Weight int
}

// Done 是选点反馈闭包：err=nil 表示这次转发成功。
type Done func(err error)

// Resolver 是网关消费的选点接口。
type Resolver interface {
	// Pick 为 service（Consul 注册名）选一个实例。
	Pick(service string) (Instance, Done, error)
	// Ready 报告是否已有至少一个服务的可用快照（readyz 条件之一）。
	Ready() bool
}

// Lister 是目录后端的抽象缝：Consul 实现之外，测试与将来的 K8s DNS 实现都从这里进。
type Lister interface {
	// List 阻塞式列出 service 的健康实例；index 用于 blocking query 续传。
	List(ctx context.Context, service string, index uint64) ([]Instance, uint64, error)
}

// 错误分类。
var (
	ErrNoInstance = errors.New("resolver: no healthy instance")
	ErrUnknownSvc = errors.New("resolver: service not watched")
)

const (
	// cooldown 是被动健康冷却：连续 failThreshold 次失败后实例休眠该时长。
	cooldown      = 5 * time.Second
	failThreshold = 3
)

type node struct {
	inst     Instance
	inflight atomic.Int64
	fails    atomic.Int32
	downTill atomic.Int64 // unix nano；0=健康
}

func (n *node) healthy(now time.Time) bool {
	return n.downTill.Load() < now.UnixNano()
}

func (n *node) feedback(err error, now time.Time) {
	n.inflight.Add(-1)
	if err == nil {
		n.fails.Store(0)
		return
	}
	if n.fails.Add(1) >= failThreshold {
		n.downTill.Store(now.Add(cooldown).UnixNano())
		n.fails.Store(0)
	}
}

type serviceState struct {
	mu    sync.RWMutex
	nodes map[string]*node // key=Addr
	// emptyStreak 记录 Consul 连续空结果次数（ACL 静默失败告警用）。
	emptyStreak atomic.Int64
}

// snapshotSwap 增量应用新实例清单：保留仍存在实例的状态，新增补零值，缺失剔除。
func (s *serviceState) snapshotSwap(list []Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]*node, len(list))
	for _, inst := range list {
		if old, ok := s.nodes[inst.Addr]; ok {
			old.inst = inst // weight 可能变化
			next[inst.Addr] = old
			continue
		}
		next[inst.Addr] = &node{inst: inst}
	}
	s.nodes = next
}

func (s *serviceState) candidates(now time.Time) []*node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*node, 0, len(s.nodes))
	for _, n := range s.nodes {
		if n.healthy(now) {
			out = append(out, n)
		}
	}
	// 全部处于冷却时退化为全量候选：宁可打冷却节点也不报无实例。
	if len(out) == 0 && len(s.nodes) > 0 {
		for _, n := range s.nodes {
			out = append(out, n)
		}
	}
	return out
}

// Watching 是基于 Lister 的 Resolver 实现。
type Watching struct {
	lister   Lister
	services map[string]*serviceState
	readyN   atomic.Int64
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	now      func() time.Time

	// OnEmptyResult 在某服务连续收到空结果时回调（告警接线；可为 nil）。
	OnEmptyResult func(service string, streak int64)
}

// NewWatching 为 services（Consul 注册名集合）各起一个 Watch 循环。
func NewWatching(lister Lister, services []string) *Watching {
	ctx, cancel := context.WithCancel(context.Background())
	w := &Watching{
		lister:   lister,
		services: make(map[string]*serviceState, len(services)),
		cancel:   cancel,
		now:      time.Now,
	}
	for _, svc := range services {
		st := &serviceState{nodes: map[string]*node{}}
		w.services[svc] = st
		w.wg.Add(1)
		go w.watch(ctx, svc, st)
	}
	return w
}

func (w *Watching) watch(ctx context.Context, service string, st *serviceState) {
	defer w.wg.Done()
	var index uint64
	backoff := time.Second
	hadSnapshot := false
	for {
		list, next, err := w.lister.List(ctx, service, index)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		index = next

		if len(list) == 0 {
			streak := st.emptyStreak.Add(1)
			if w.OnEmptyResult != nil {
				w.OnEmptyResult(service, streak)
			}
			// 保留上一份非空快照（Consul ACL 缺 token 会 200 空列表）。
			if hadSnapshot {
				continue
			}
			continue
		}
		st.emptyStreak.Store(0)
		if !hadSnapshot {
			hadSnapshot = true
			w.readyN.Add(1)
		}
		st.snapshotSwap(list)
	}
}

// Pick 实现 Resolver：P2C（按在途数），weight 作为平手时的偏置。
func (w *Watching) Pick(service string) (Instance, Done, error) {
	st, ok := w.services[service]
	if !ok {
		return Instance{}, nil, fmt.Errorf("%w: %s", ErrUnknownSvc, service)
	}
	now := w.now()
	cands := st.candidates(now)
	if len(cands) == 0 {
		return Instance{}, nil, fmt.Errorf("%w: %s", ErrNoInstance, service)
	}
	chosen := pickP2C(cands)
	chosen.inflight.Add(1)
	var once sync.Once
	done := Done(func(err error) {
		once.Do(func() { chosen.feedback(err, w.now()) })
	})
	return chosen.inst, done, nil
}

func pickP2C(cands []*node) *node {
	if len(cands) == 1 {
		return cands[0]
	}
	i := rand.IntN(len(cands))
	j := rand.IntN(len(cands) - 1)
	if j >= i {
		j++
	}
	a, b := cands[i], cands[j]
	la, lb := a.inflight.Load(), b.inflight.Load()
	if la == lb {
		// 平手按 weight 偏置（0 视为 1）。
		wa, wb := max(a.inst.Weight, 1), max(b.inst.Weight, 1)
		if rand.IntN(wa+wb) < wa {
			return a
		}
		return b
	}
	if la < lb {
		return a
	}
	return b
}

// Ready 实现 Resolver。
func (w *Watching) Ready() bool {
	return w.readyN.Load() > 0
}

// Close 停止全部 Watch 循环。
func (w *Watching) Close() {
	w.cancel()
	w.wg.Wait()
}
