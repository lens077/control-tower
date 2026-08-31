package presence

import "testing"

func TestRedisBackendKeySeparatesWriterProcesses(t *testing.T) {
	backend := &redisBackend{prefix: "config-center:presence:"}
	identity := Identity{Name: "control-tower-gateway", Instance: "gateway-1"}
	first := Connection{Identity: identity, source: "config-pod-a"}
	second := Connection{Identity: identity, source: "config-pod-b"}

	if backend.key(first) == backend.key(second) {
		t.Fatal("different Config Center writers must not overwrite the same client presence key")
	}
}
