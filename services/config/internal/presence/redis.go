package presence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Backend holds only ephemeral client-presence state. Implementations must
// never persist credentials or configuration values.
type Backend interface {
	Store(context.Context, Connection, time.Duration) error
	List(context.Context) ([]Connection, error)
}

type redisBackend struct {
	client *redis.Client
	prefix string
}

func newRedisBackend(client *redis.Client, prefix string) Backend {
	return &redisBackend{client: client, prefix: strings.TrimSuffix(prefix, ":") + ":"}
}

func (b *redisBackend) Store(ctx context.Context, connection Connection, ttl time.Duration) error {
	contents, err := json.Marshal(connection)
	if err != nil {
		return fmt.Errorf("encode client presence: %w", err)
	}
	return b.client.Set(ctx, b.key(connection.Identity), contents, ttl).Err()
}

func (b *redisBackend) List(ctx context.Context) ([]Connection, error) {
	var (
		cursor uint64
		keys   []string
	)
	for {
		page, next, err := b.client.Scan(ctx, cursor, b.prefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan client presence: %w", err)
		}
		keys = append(keys, page...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}

	values, err := b.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read client presence: %w", err)
	}
	connections := make([]Connection, 0, len(values))
	for _, value := range values {
		contents, ok := value.(string)
		if !ok {
			continue
		}
		var connection Connection
		if err := json.Unmarshal([]byte(contents), &connection); err != nil || !valid(connection.Identity) {
			continue
		}
		connection.Targets = append([]Target(nil), connection.Targets...)
		connections = append(connections, connection)
	}
	sortConnections(connections)
	if len(connections) > maxClients {
		connections = connections[:maxClients]
	}
	return connections, nil
}

func (b *redisBackend) key(identity Identity) string {
	// Base64 makes identity values safe Redis key components without leaking a
	// delimiter convention into service and instance names.
	encoded := base64.RawURLEncoding.EncodeToString([]byte(identity.key()))
	return b.prefix + encoded
}
