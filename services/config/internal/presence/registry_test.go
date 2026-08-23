package presence

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryBackend struct {
	connections map[string]Connection
	ttl         time.Duration
	err         error
}

func (b *memoryBackend) Store(_ context.Context, connection Connection, ttl time.Duration) error {
	if b.err != nil {
		return b.err
	}
	if b.connections == nil {
		b.connections = make(map[string]Connection)
	}
	b.ttl = ttl
	b.connections[connection.Identity.key()] = connection
	return nil
}

func (b *memoryBackend) List(_ context.Context) ([]Connection, error) {
	if b.err != nil {
		return nil, b.err
	}
	connections := make([]Connection, 0, len(b.connections))
	for _, connection := range b.connections {
		connections = append(connections, connection)
	}
	return connections, nil
}

func TestRegistryTracksReadWatchAndDisconnect(t *testing.T) {
	r := NewRegistry()
	identity := Identity{Name: "cart-service", Instance: "cart-1", Version: "dev"}
	target := Target{Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml"}

	r.RecordRead(identity, target)
	r.StartWatch(identity, []Target{target})
	r.TouchWatch(identity)

	connections := r.List()
	if len(connections) != 1 {
		t.Fatalf("connections = %d, want 1", len(connections))
	}
	connection := connections[0]
	if !connection.Watching || connection.LastReadAt.IsZero() || connection.LastWatchAt.IsZero() {
		t.Fatalf("connection = %#v, want active read/watch timestamps", connection)
	}
	if len(connection.Targets) != 1 || connection.Targets[0] != target {
		t.Fatalf("targets = %#v, want %#v", connection.Targets, target)
	}

	r.StopWatch(identity, "heartbeat_failed")
	connection = r.List()[0]
	if connection.Watching || connection.DisconnectedAt.IsZero() || connection.LastDisconnectReason != "heartbeat_failed" {
		t.Fatalf("connection after disconnect = %#v", connection)
	}
}

func TestRegistryIgnoresUnnamedClients(t *testing.T) {
	r := NewRegistry()
	r.RecordRead(Identity{}, Target{Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml"})
	r.StartWatch(Identity{}, nil)
	if got := r.List(); len(got) != 0 {
		t.Fatalf("connections = %#v, want no unnamed clients", got)
	}
}

func TestRegistryUsesSharedBackendAndFallsBackWhenItIsUnavailable(t *testing.T) {
	backend := &memoryBackend{}
	registry := NewRegistry()
	registry.backend = backend
	registry.backendEnabled = true
	registry.backendTTL = 90 * time.Second

	identity := Identity{Name: "cart-service", Instance: "cart-1", Version: "dev"}
	target := Target{Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml"}
	registry.RecordRead(identity, target)
	registry.StartWatch(identity, []Target{target})

	if got := registry.Mode(); got != ModeRedisTTL {
		t.Fatalf("mode = %q, want %q", got, ModeRedisTTL)
	}
	if got := backend.ttl; got != 90*time.Second {
		t.Fatalf("backend TTL = %s, want 90s", got)
	}

	// A second Config Center process would share the backend but have no local state.
	other := NewRegistry()
	other.backend = backend
	other.backendEnabled = true
	other.backendTTL = 90 * time.Second
	connections := other.List()
	if len(connections) != 1 || !connections[0].Watching {
		t.Fatalf("shared connections = %#v, want Cart Watch", connections)
	}

	backend.err = errors.New("redis unavailable")
	connections = registry.List()
	if len(connections) != 1 || !connections[0].Watching {
		t.Fatalf("fallback connections = %#v, want local Cart Watch", connections)
	}
	if got := registry.Mode(); got != ModeRedisTTLDegraded {
		t.Fatalf("mode = %q, want %q", got, ModeRedisTTLDegraded)
	}
}
