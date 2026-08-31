// Package presence tracks configuration clients connected to one server instance.
package presence

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lens077/control-tower/services/config/internal/pkg/config"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const maxClients = 512

type Identity struct {
	Name     string
	Instance string
	Version  string
}

type Target struct {
	Namespace   string
	Environment string
	Key         string
}

type Connection struct {
	Identity
	// source 区分向共享 Backend 写入同一客户端的 Config Center 进程，不进入 API 或 JSON。
	source               string
	Targets              []Target
	Watching             bool
	ConnectedAt          time.Time
	LastReadAt           time.Time
	LastWatchAt          time.Time
	DisconnectedAt       time.Time
	LastDisconnectReason string
}

type Mode string

const (
	ModeLocal            Mode = "local"
	ModeRedisTTL         Mode = "redis_ttl"
	ModeRedisTTLDegraded Mode = "redis_ttl_degraded"
	ModeHeader                = "x-config-center-presence-mode"
)

type Registry struct {
	mu             sync.Mutex
	persistMu      sync.Mutex
	source         string
	clients        map[string]Connection
	watchStreams   map[string]int
	watchTargets   map[string]map[Target]int
	backend        Backend
	backendTTL     time.Duration
	backendEnabled bool
	backendFailed  bool
	log            *zap.Logger
}

var Module = fx.Module("presence", fx.Provide(NewConfiguredRegistry))

func NewRegistry() *Registry {
	return &Registry{
		source:       uuid.NewString(),
		clients:      make(map[string]Connection),
		watchStreams: make(map[string]int),
		watchTargets: make(map[string]map[Target]int),
	}
}

func NewConfiguredRegistry(settings config.PresenceSettings, client *redis.Client, logger *zap.Logger) *Registry {
	registry := NewRegistry()
	registry.log = logger.Named("presence")
	if settings.RedisEnabled {
		registry.backend = newRedisBackend(client, settings.RedisKeyPrefix)
		registry.backendTTL = settings.RedisTTL
		registry.backendEnabled = true
		registry.log.Info("Redis TTL client-presence aggregation enabled",
			zap.String("key_prefix", settings.RedisKeyPrefix),
			zap.Duration("ttl", settings.RedisTTL))
	}
	return registry
}

func (i Identity) key() string { return i.Name + "\x00" + i.Instance }

func valid(i Identity) bool { return i.Name != "" }

func (r *Registry) RecordRead(identity Identity, target Target) {
	if !valid(identity) {
		return
	}
	key := identity.key()
	r.mu.Lock()
	r.evictIfNeeded(key)
	item := r.clients[key]
	item.Identity = identity
	item.source = r.source
	if item.Watching {
		item.Targets = activeTargets(r.watchTargets[key])
	} else {
		item.Targets = []Target{target}
	}
	item.LastReadAt = time.Now()
	r.clients[key] = item
	r.mu.Unlock()
	r.persistCurrent(key)
}

func (r *Registry) StartWatch(identity Identity, targets []Target) {
	if !valid(identity) {
		return
	}
	key := identity.key()
	r.mu.Lock()
	r.evictIfNeeded(key)
	now := time.Now()
	item := r.clients[key]
	item.Identity = identity
	item.source = r.source
	if r.watchStreams[key] == 0 {
		item.ConnectedAt = now
	}
	r.watchStreams[key]++
	counts := r.watchTargets[key]
	if counts == nil {
		counts = make(map[Target]int)
		r.watchTargets[key] = counts
	}
	for _, target := range targets {
		counts[target]++
	}
	item.Targets = activeTargets(counts)
	item.Watching = true
	item.LastWatchAt = now
	item.DisconnectedAt = time.Time{}
	item.LastDisconnectReason = ""
	r.clients[key] = item
	r.mu.Unlock()
	r.persistCurrent(key)
}

func (r *Registry) TouchWatch(identity Identity) {
	if !valid(identity) {
		return
	}
	key := identity.key()
	r.mu.Lock()
	item, ok := r.clients[key]
	if !ok || !item.Watching {
		r.mu.Unlock()
		return
	}
	item.LastWatchAt = time.Now()
	r.clients[key] = item
	r.mu.Unlock()
	r.persistCurrent(key)
}

func (r *Registry) StopWatch(identity Identity, targets []Target, reason string) {
	if !valid(identity) {
		return
	}
	key := identity.key()
	r.mu.Lock()
	item, ok := r.clients[key]
	if !ok || r.watchStreams[key] == 0 {
		r.mu.Unlock()
		return
	}
	counts := r.watchTargets[key]
	for _, target := range targets {
		if counts[target] <= 1 {
			delete(counts, target)
		} else {
			counts[target]--
		}
	}
	r.watchStreams[key]--
	if r.watchStreams[key] > 0 {
		item.Targets = activeTargets(counts)
		item.Watching = true
		item.DisconnectedAt = time.Time{}
		item.LastDisconnectReason = ""
	} else {
		delete(r.watchStreams, key)
		delete(r.watchTargets, key)
		item.Watching = false
		item.DisconnectedAt = time.Now()
		item.LastDisconnectReason = reason
	}
	r.clients[key] = item
	r.mu.Unlock()
	r.persistCurrent(key)
}

func (r *Registry) List() []Connection {
	if r.backendEnabled {
		ctx, cancel := contextWithTimeout()
		defer cancel()
		connections, err := r.backend.List(ctx)
		if err == nil {
			r.setBackendHealthy()
			return mergeConnections(connections)
		}
		r.setBackendFailed(err)
	}
	return r.listLocal()
}

func (r *Registry) Mode() Mode {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.backendEnabled {
		return ModeLocal
	}
	if r.backendFailed {
		return ModeRedisTTLDegraded
	}
	return ModeRedisTTL
}

func (r *Registry) listLocal() []Connection {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]Connection, 0, len(r.clients))
	for _, item := range r.clients {
		item.Targets = append([]Target(nil), item.Targets...)
		items = append(items, item)
	}
	sortConnections(items)
	return items
}

func sortConnections(items []Connection) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Watching != items[j].Watching {
			return items[i].Watching
		}
		return items[i].LastReadAt.After(items[j].LastReadAt)
	})
}

func mergeConnections(items []Connection) []Connection {
	groups := make(map[string][]Connection)
	for _, item := range items {
		if valid(item.Identity) {
			groups[item.Identity.key()] = append(groups[item.Identity.key()], item)
		}
	}
	merged := make([]Connection, 0, len(groups))
	for _, group := range groups {
		merged = append(merged, mergeConnectionGroup(group))
	}
	sortConnections(merged)
	if len(merged) > maxClients {
		merged = merged[:maxClients]
	}
	return merged
}

func mergeConnectionGroup(group []Connection) Connection {
	var (
		result          Connection
		latestIdentity  time.Time
		activeConnected time.Time
		allConnected    time.Time
		active          = make(map[Target]int)
		all             = make(map[Target]int)
	)
	for _, item := range group {
		activity := latestTime(item.LastReadAt, item.LastWatchAt, item.DisconnectedAt, item.ConnectedAt)
		if result.Name == "" || activity.After(latestIdentity) {
			result.Identity = item.Identity
			latestIdentity = activity
		}
		allConnected = earliestTime(allConnected, item.ConnectedAt)
		if item.LastReadAt.After(result.LastReadAt) {
			result.LastReadAt = item.LastReadAt
		}
		if item.LastWatchAt.After(result.LastWatchAt) {
			result.LastWatchAt = item.LastWatchAt
		}
		if item.DisconnectedAt.After(result.DisconnectedAt) {
			result.DisconnectedAt = item.DisconnectedAt
			result.LastDisconnectReason = item.LastDisconnectReason
		}
		for _, target := range item.Targets {
			all[target]++
			if item.Watching {
				active[target]++
			}
		}
		if item.Watching {
			result.Watching = true
			activeConnected = earliestTime(activeConnected, item.ConnectedAt)
		}
	}
	if result.Watching {
		result.Targets = activeTargets(active)
		result.ConnectedAt = activeConnected
		result.DisconnectedAt = time.Time{}
		result.LastDisconnectReason = ""
	} else {
		result.Targets = activeTargets(all)
		result.ConnectedAt = allConnected
	}
	return result
}

func latestTime(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func earliestTime(current, candidate time.Time) time.Time {
	if candidate.IsZero() || (!current.IsZero() && !candidate.Before(current)) {
		return current
	}
	return candidate
}

func activeTargets(counts map[Target]int) []Target {
	targets := make([]Target, 0, len(counts))
	for target, count := range counts {
		if count > 0 {
			targets = append(targets, target)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Namespace != targets[j].Namespace {
			return targets[i].Namespace < targets[j].Namespace
		}
		if targets[i].Environment != targets[j].Environment {
			return targets[i].Environment < targets[j].Environment
		}
		return targets[i].Key < targets[j].Key
	})
	return targets
}

func (r *Registry) persistCurrent(key string) {
	if !r.backendEnabled {
		return
	}
	// 同一进程里的并发 Watch 会连续更新同一客户端。串行后再读取当前快照，
	// 避免较早的 Store 晚到，把已经合并的 targets 覆盖回旧值。
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	r.mu.Lock()
	item, ok := r.clients[key]
	r.mu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := contextWithTimeout()
	defer cancel()
	if err := r.backend.Store(ctx, item, r.backendTTL); err != nil {
		r.setBackendFailed(err)
		return
	}
	r.setBackendHealthy()
}

func (r *Registry) setBackendFailed(err error) {
	r.mu.Lock()
	firstFailure := !r.backendFailed
	r.backendFailed = true
	r.mu.Unlock()
	if firstFailure && r.log != nil {
		r.log.Warn("Redis TTL presence aggregation unavailable; using process-local fallback", zap.Error(err))
	}
}

func (r *Registry) setBackendHealthy() {
	r.mu.Lock()
	wasFailed := r.backendFailed
	r.backendFailed = false
	r.mu.Unlock()
	if wasFailed && r.log != nil {
		r.log.Info("Redis TTL presence aggregation recovered")
	}
}

func contextWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Second)
}

func (r *Registry) evictIfNeeded(keep string) {
	if len(r.clients) < maxClients {
		return
	}
	oldestKey := keep
	var oldest time.Time
	for key, item := range r.clients {
		if key == keep || item.Watching {
			continue
		}
		if oldest.IsZero() || item.LastReadAt.Before(oldest) {
			oldestKey, oldest = key, item.LastReadAt
		}
	}
	if oldestKey != keep {
		delete(r.clients, oldestKey)
		delete(r.watchStreams, oldestKey)
		delete(r.watchTargets, oldestKey)
	}
}
