package session

import (
	"context"
	"sync"
	"time"
)

// MemoryStore 是进程内会话存储：供单测与「无 Dragonfly 的本地开发」使用。
// 生产禁用——网关多副本时各副本会话互不可见。
type MemoryStore struct {
	mu     sync.RWMutex
	ttl    TTL
	now    func() time.Time
	data   map[string]*entry
	states map[string]stateEntry
}

type entry struct {
	sess      Session
	idleUntil time.Time
}

type stateEntry struct {
	payload []byte
	exp     time.Time
}

// NewMemoryStore 构造内存存储。
func NewMemoryStore(ttl TTL) *MemoryStore {
	return &MemoryStore{ttl: ttl, now: time.Now, data: map[string]*entry{}}
}

var _ Store = (*MemoryStore)(nil)

func (m *MemoryStore) Create(_ context.Context, s *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	m.data[s.ID] = &entry{sess: cp, idleUntil: m.now().Add(m.ttl.Idle)}
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[id]
	now := m.now()
	if !ok || now.After(e.idleUntil) || e.sess.Expired(m.ttl, now) {
		delete(m.data, id)
		return nil, ErrNotFound
	}
	e.idleUntil = now.Add(m.ttl.Idle) // 访问即续期空闲窗
	e.sess.LastSeenAt = now
	cp := e.sess
	return &cp, nil
}

func (m *MemoryStore) Save(_ context.Context, s *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[s.ID]
	if !ok {
		return ErrNotFound
	}
	cp := *s
	e.sess = cp
	e.idleUntil = m.now().Add(m.ttl.Idle)
	return nil
}

func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, id)
	return nil
}

func (m *MemoryStore) ListByUser(_ context.Context, sub string) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := m.now()
	var out []*Session
	for _, e := range m.data {
		if e.sess.Sub == sub && !now.After(e.idleUntil) && !e.sess.Expired(m.ttl, now) {
			cp := e.sess
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *MemoryStore) DeleteByUser(_ context.Context, sub string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, e := range m.data {
		if e.sess.Sub == sub {
			delete(m.data, id)
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) Ping(context.Context) error { return nil }

func (m *MemoryStore) PutState(_ context.Context, state string, payload []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.states == nil {
		m.states = map[string]stateEntry{}
	}
	m.states[state] = stateEntry{payload: append([]byte(nil), payload...), exp: m.now().Add(ttl)}
	return nil
}

func (m *MemoryStore) TakeState(_ context.Context, state string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.states[state]
	if !ok {
		return nil, ErrNotFound
	}
	delete(m.states, state) // 单次使用，防重放
	if m.now().After(e.exp) {
		return nil, ErrNotFound
	}
	return e.payload, nil
}
