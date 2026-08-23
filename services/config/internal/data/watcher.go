package data

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lens077/control-tower/services/config/internal/biz"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// notifyChannel Postgres LISTEN/NOTIFY 的通道名。
const notifyChannel = "config_changed"

const (
	// subBuffer 每个订阅者的事件缓冲。配置变更是低频事件,16 已经很宽裕;
	// 真溢出说明订阅方卡住了,此时丢事件也比阻塞监听协程好。
	subBuffer = 16
	// 监听连接掉线后的重连退避区间
	reconnectMinBackoff = time.Second
	reconnectMaxBackoff = 30 * time.Second
)

// changePayload pg_notify 的载荷。
//
// 只带定位信息:NOTIFY 的 payload 有 8000 字节上限,整份配置迟早会超;
// 而且把 value(可能是密钥)塞进通知通道没有任何好处 —— 订阅方回查一次即可。
type changePayload struct {
	Namespace   string `json:"ns"`
	Environment string `json:"env"`
	Key         string `json:"key"`
	Version     int32  `json:"ver"`
	Deleted     bool   `json:"del,omitempty"`
}

// notify 在调用方的事务内发出变更通知。
//
// 必须在事务内发:pg_notify 的投递与事务提交绑定 —— 回滚不会发,提交才会发。
// 放到 Commit 之后则存在「通知已出去、数据还没落库」和「提交成功、进程挂了通知丢失」
// 两个窗口,前者会让订阅方读到旧值并当成新值缓存下来。
func notify(ctx context.Context, tx pgx.Tx, p changePayload) error {
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal notify payload: %w", err)
	}
	if _, err := tx.Exec(ctx, "select pg_notify($1, $2)", notifyChannel, string(b)); err != nil {
		return fmt.Errorf("pg_notify: %w", err)
	}
	return nil
}

var _ biz.ConfigWatcher = (*Watcher)(nil)

type subscriber struct {
	namespace   string
	environment string
	keys        map[string]struct{} // 空 map 表示订阅该 ns/env 下全部 key
	ch          chan biz.ChangeEvent
}

func (s *subscriber) wants(ev biz.ChangeEvent) bool {
	if s.namespace != ev.Namespace || s.environment != ev.Environment {
		return false
	}
	if len(s.keys) == 0 {
		return true
	}
	_, ok := s.keys[ev.Key]
	return ok
}

// Watcher 独占一条 Postgres 连接 LISTEN 变更通知,并在进程内扇出给各订阅者。
//
// 为什么用 LISTEN/NOTIFY 而不是进程内直接投递:config-service 扩到多副本后,
// 写在副本 A 的变更必须能推给连在副本 B 上的客户端。走数据库让「谁写的」变得
// 无关紧要,而且发布点就在写入事务里,不会与提交结果不一致。
type Watcher struct {
	pool *pgxpool.Pool
	log  *zap.Logger

	mu      sync.Mutex
	subs    map[*subscriber]struct{}
	healthy bool // 监听连接是否可用;false 时新订阅直接拿到已关闭的 channel
}

func NewWatcher(lc fx.Lifecycle, pool *pgxpool.Pool, logger *zap.Logger) biz.ConfigWatcher {
	w := &Watcher{
		pool: pool,
		log:  logger.Named("configWatcher"),
		subs: make(map[*subscriber]struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go w.run(ctx)
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			return nil
		},
	})
	return w
}

// Subscribe 注册一个订阅者。cancel 幂等,调用后不再收到事件。
func (w *Watcher) Subscribe(namespace, environment string, keys []string) (<-chan biz.ChangeEvent, func()) {
	s := &subscriber{
		namespace:   namespace,
		environment: environment,
		keys:        make(map[string]struct{}, len(keys)),
		ch:          make(chan biz.ChangeEvent, subBuffer),
	}
	for _, k := range keys {
		s.keys[k] = struct{}{}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 链路不通时直接给一个已关闭的 channel:调用方会立刻收到「流不可用」,
	// 而不是挂在一条永远静默的订阅上。
	if !w.healthy {
		close(s.ch)
		return s.ch, func() {}
	}

	w.subs[s] = struct{}{}
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			if _, ok := w.subs[s]; ok {
				delete(w.subs, s)
				close(s.ch)
			}
		})
	}
}

// publish 非阻塞扇出。任何一个订阅者卡住都不能拖慢监听协程,
// 因此缓冲满了就丢事件并告警 —— 丢事件的后果由客户端重连重取快照兜底。
func (w *Watcher) publish(ev biz.ChangeEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for s := range w.subs {
		if !s.wants(ev) {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			w.log.Warn("订阅者缓冲已满,丢弃变更事件",
				zap.String("namespace", ev.Namespace),
				zap.String("environment", ev.Environment),
				zap.String("key", ev.Key),
			)
		}
	}
}

// failAll 关闭全部订阅,通知调用方下发链路已断。
func (w *Watcher) failAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.healthy = false
	for s := range w.subs {
		close(s.ch)
		delete(w.subs, s)
	}
}

func (w *Watcher) setHealthy() {
	w.mu.Lock()
	w.healthy = true
	w.mu.Unlock()
}

// run 维持 LISTEN 连接,断线按指数退避重连。
func (w *Watcher) run(ctx context.Context) {
	backoff := reconnectMinBackoff
	for {
		if err := w.listen(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("配置变更监听中断,准备重连", zap.Error(err), zap.Duration("backoff", backoff))
		}
		// 无论因何退出,先让所有订阅者感知到断流,由客户端重连重取快照补齐窗口期的变更。
		w.failAll()

		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > reconnectMaxBackoff {
			backoff = reconnectMaxBackoff
		}
	}
}

// listen 建立专用连接并阻塞消费通知,直到出错或 ctx 取消。
func (w *Watcher) listen(ctx context.Context) error {
	// 用连接池的连接参数单独拨一条连接,而不是 Acquire:LISTEN 是会话级状态,
	// 长期占着池里的连接会挤掉正常查询的名额。
	connCfg := w.pool.Config().ConnConfig.Copy()
	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return fmt.Errorf("dial listen conn: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	if _, err = conn.Exec(ctx, "listen "+notifyChannel); err != nil {
		return fmt.Errorf("listen %s: %w", notifyChannel, err)
	}

	w.setHealthy()
	w.log.Info("配置变更监听已建立", zap.String("channel", notifyChannel))

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait notification: %w", err)
		}
		var p changePayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			// 载荷解不开说明有人往同一通道发了别的东西,跳过即可,不必断流
			w.log.Warn("忽略无法解析的变更通知", zap.String("payload", n.Payload), zap.Error(err))
			continue
		}
		w.publish(biz.ChangeEvent{
			Namespace:   p.Namespace,
			Environment: p.Environment,
			Key:         p.Key,
			Version:     p.Version,
			Deleted:     p.Deleted,
		})
	}
}
