package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore 以 Dragonfly（Redis 协议）承载会话。
//
// 键布局：
//   - sess:{id}   会话 JSON，键 TTL = 空闲上限（访问即续期）
//   - user:{sub}  该用户的 session id 集合，TTL = 绝对上限；用于会话清单与整户踢出
//
// 绝对上限不靠键 TTL 保证（空闲续期会顶高它），由 Session.Expired 在读取时判定。
type RedisStore struct {
	rdb *redis.Client
	ttl TTL
	now func() time.Time
}

// NewRedisStore 构造存储。
func NewRedisStore(rdb *redis.Client, ttl TTL) *RedisStore {
	return &RedisStore{rdb: rdb, ttl: ttl, now: time.Now}
}

var _ Store = (*RedisStore)(nil)

func sessKey(id string) string  { return "sess:" + id }
func userKey(sub string) string { return "user:" + sub }

func (r *RedisStore) Create(ctx context.Context, s *Session) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	pipe := r.rdb.TxPipeline()
	pipe.Set(ctx, sessKey(s.ID), b, r.ttl.Idle)
	pipe.SAdd(ctx, userKey(s.Sub), s.ID)
	pipe.Expire(ctx, userKey(s.Sub), r.ttl.Absolute)
	_, err = pipe.Exec(ctx)
	return err
}

func (r *RedisStore) Get(ctx context.Context, id string) (*Session, error) {
	b, err := r.rdb.Get(ctx, sessKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("session: get: %w", err)
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("session: decode: %w", err)
	}
	now := r.now()
	if s.Expired(r.ttl, now) {
		_ = r.Delete(ctx, id) // 越过绝对上限即清理
		return nil, ErrNotFound
	}
	// 访问即续期空闲窗（不动绝对上限）。
	r.rdb.Expire(ctx, sessKey(id), r.ttl.Idle)
	s.LastSeenAt = now
	return &s, nil
}

func (r *RedisStore) Save(ctx context.Context, s *Session) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.rdb.Set(ctx, sessKey(s.ID), b, r.ttl.Idle).Err()
}

func (r *RedisStore) Delete(ctx context.Context, id string) error {
	// 先读出 sub 才能清二级索引；读不到就只删主键。
	if b, err := r.rdb.Get(ctx, sessKey(id)).Bytes(); err == nil {
		var s Session
		if json.Unmarshal(b, &s) == nil && s.Sub != "" {
			r.rdb.SRem(ctx, userKey(s.Sub), id)
		}
	}
	return r.rdb.Del(ctx, sessKey(id)).Err()
}

func (r *RedisStore) ListByUser(ctx context.Context, sub string) ([]*Session, error) {
	ids, err := r.rdb.SMembers(ctx, userKey(sub)).Result()
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, id := range ids {
		s, err := r.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			r.rdb.SRem(ctx, userKey(sub), id) // 顺手清悬挂索引
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (r *RedisStore) DeleteByUser(ctx context.Context, sub string) (int, error) {
	ids, err := r.rdb.SMembers(ctx, userKey(sub)).Result()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if r.rdb.Del(ctx, sessKey(id)).Err() == nil {
			n++
		}
	}
	r.rdb.Del(ctx, userKey(sub))
	return n, nil
}

func (r *RedisStore) Ping(ctx context.Context) error { return r.rdb.Ping(ctx).Err() }

func stateKey(s string) string { return "oauthstate:" + s }

func (r *RedisStore) PutState(ctx context.Context, state string, payload []byte, ttl time.Duration) error {
	return r.rdb.Set(ctx, stateKey(state), payload, ttl).Err()
}

func (r *RedisStore) TakeState(ctx context.Context, state string) ([]byte, error) {
	// GETDEL 保证单次使用：并发重放拿不到第二次。
	b, err := r.rdb.GetDel(ctx, stateKey(state)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("session: take state: %w", err)
	}
	return b, nil
}
