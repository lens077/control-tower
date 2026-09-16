package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	configv1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/api/config/v1/configv1connect"
)

// fakeService 复刻 Config Center 的写入语义：同 key 重复写要涨版本号。
// CLI 的「created / updated / verified」输出全靠它来断言。
type fakeService struct {
	configv1connect.UnimplementedConfigServiceHandler

	mu      sync.Mutex
	entries map[string]*configv1.ConfigEntry
	puts    []*configv1.PutKeyRequest
}

func newFakeService() *fakeService {
	return &fakeService{entries: map[string]*configv1.ConfigEntry{}}
}

func entryKey(namespace, environment, key string) string {
	return namespace + "\x00" + environment + "\x00" + key
}

func (f *fakeService) GetKey(_ context.Context, request *connect.Request[configv1.GetKeyRequest]) (*connect.Response[configv1.GetKeyResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[entryKey(request.Msg.GetNamespace(), request.Msg.GetEnvironment(), request.Msg.GetKey())]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("key not found"))
	}
	return connect.NewResponse(&configv1.GetKeyResponse{Entry: entry}), nil
}

func (f *fakeService) PutKey(_ context.Context, request *connect.Request[configv1.PutKeyRequest]) (*connect.Response[configv1.PutKeyResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, request.Msg)
	id := entryKey(request.Msg.GetNamespace(), request.Msg.GetEnvironment(), request.Msg.GetKey())
	version := int32(1)
	if previous, ok := f.entries[id]; ok {
		version = previous.GetVersion() + 1
	}
	entry := &configv1.ConfigEntry{
		Namespace:   request.Msg.GetNamespace(),
		Environment: request.Msg.GetEnvironment(),
		Key:         request.Msg.GetKey(),
		Format:      request.Msg.GetFormat(),
		Value:       request.Msg.GetValue(),
		Version:     version,
	}
	f.entries[id] = entry
	return connect.NewResponse(&configv1.PutKeyResponse{Entry: entry}), nil
}

func (f *fakeService) ListKeys(_ context.Context, request *connect.Request[configv1.ListKeysRequest]) (*connect.Response[configv1.ListKeysResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var entries []*configv1.ConfigEntry
	for _, entry := range f.entries {
		if entry.GetNamespace() == request.Msg.GetNamespace() && entry.GetEnvironment() == request.Msg.GetEnvironment() {
			entries = append(entries, entry)
		}
	}
	return connect.NewResponse(&configv1.ListKeysResponse{Entries: entries}), nil
}

func startFake(t *testing.T) (*fakeService, string) {
	t.Helper()
	service := newFakeService()
	mux := http.NewServeMux()
	mux.Handle(configv1connect.NewConfigServiceHandler(service))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Setenv("CONFIG_CENTER_SERVICE_TOKEN", "ct_operator")
	return service, server.URL
}

func writeTempValue(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write temp value: %v", err)
	}
	return path
}

func TestPutCreatesThenUpdates(t *testing.T) {
	service, endpoint := startFake(t)
	file := writeTempValue(t, "jaeger.yaml", "apiVersion: 1\n")

	args := []string{
		"put", "-endpoint", endpoint,
		"-namespace", "observability", "-environment", "prod",
		"-key", "grafana/datasources/jaeger",
		"-file", file, "-comment", "接入 Jaeger",
	}

	var stdout bytes.Buffer
	if err := run(context.Background(), args, &stdout, &stdout); err != nil {
		t.Fatalf("first put: %v (output %s)", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "created observability/prod/grafana/datasources/jaeger") {
		t.Fatalf("expected a created line, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "verified") {
		t.Fatalf("expected a read-back verification line, got %q", stdout.String())
	}
	if got := service.puts[0].GetFormat(); got != configv1.ConfigFormat_CONFIG_FORMAT_YAML {
		t.Fatalf("format should be inferred from the .yaml extension, got %v", got)
	}
	if service.puts[0].GetComment() != "接入 Jaeger" {
		t.Fatalf("comment not forwarded: %q", service.puts[0].GetComment())
	}

	stdout.Reset()
	if err := run(context.Background(), args, &stdout, &stdout); err != nil {
		t.Fatalf("second put: %v", err)
	}
	if !strings.Contains(stdout.String(), "updated ") || !strings.Contains(stdout.String(), "version=2") {
		t.Fatalf("expected an updated line at version 2, got %q", stdout.String())
	}
}

func TestPutDryRunDoesNotWrite(t *testing.T) {
	service, endpoint := startFake(t)
	file := writeTempValue(t, "jaeger.yaml", "apiVersion: 1\n")

	var stdout bytes.Buffer
	err := run(context.Background(), []string{
		"put", "-endpoint", endpoint, "-dry-run",
		"-namespace", "observability", "-environment", "prod",
		"-key", "grafana/datasources/jaeger", "-file", file,
	}, &stdout, &stdout)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(stdout.String(), "would create") {
		t.Fatalf("expected a would-create line, got %q", stdout.String())
	}
	if len(service.puts) != 0 {
		t.Fatalf("dry run must not call PutKey, got %d calls", len(service.puts))
	}
}

func TestPutDryRunReportsNoChange(t *testing.T) {
	_, endpoint := startFake(t)
	file := writeTempValue(t, "jaeger.yaml", "apiVersion: 1\n")
	base := []string{
		"-endpoint", endpoint,
		"-namespace", "observability", "-environment", "prod",
		"-key", "grafana/datasources/jaeger", "-file", file,
	}

	var stdout bytes.Buffer
	if err := run(context.Background(), append([]string{"put"}, base...), &stdout, &stdout); err != nil {
		t.Fatalf("seed put: %v", err)
	}
	stdout.Reset()
	if err := run(context.Background(), append([]string{"put", "-dry-run"}, base...), &stdout, &stdout); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(stdout.String(), "already matches") {
		t.Fatalf("expected a no-change line, got %q", stdout.String())
	}
}

func TestPutRequiresExplicitFormatForUnknownExtension(t *testing.T) {
	_, endpoint := startFake(t)
	file := writeTempValue(t, "datasource.conf", "apiVersion: 1\n")

	var stdout bytes.Buffer
	err := run(context.Background(), []string{
		"put", "-endpoint", endpoint,
		"-namespace", "observability", "-environment", "prod",
		"-key", "grafana/datasources/jaeger", "-file", file,
	}, &stdout, &stdout)
	if err == nil {
		t.Fatal("expected a format requirement error for an unknown extension")
	}
	if !strings.Contains(err.Error(), "-format is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadValueFromStdinHasNoExtensionToInfer(t *testing.T) {
	value, name, err := readValue("-", strings.NewReader("apiVersion: 1\n"))
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if value != "apiVersion: 1\n" {
		t.Fatalf("unexpected stdin value %q", value)
	}
	// stdin 没有后缀，所以 -format 必须显式给；这条是 resolveFormat 报错的前提。
	if name != "" {
		t.Fatalf("stdin must not report a source name, got %q", name)
	}
	if _, err := resolveFormat("", name); err == nil {
		t.Fatal("expected resolveFormat to require an explicit format for stdin")
	}
	if _, _, err := readValue("", strings.NewReader("")); err == nil {
		t.Fatal("expected -file to be required")
	}
}

func TestGetPrintsValue(t *testing.T) {
	_, endpoint := startFake(t)
	file := writeTempValue(t, "jaeger.yaml", "apiVersion: 1\n")
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{
		"put", "-endpoint", endpoint,
		"-namespace", "observability", "-environment", "prod",
		"-key", "grafana/datasources/jaeger", "-file", file,
	}, &stdout, &stdout); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	stdout.Reset()
	if err := run(context.Background(), []string{
		"get", "-endpoint", endpoint,
		"-namespace", "observability", "-environment", "prod",
		"-key", "grafana/datasources/jaeger",
	}, &stdout, &stdout); err != nil {
		t.Fatalf("get: %v", err)
	}
	if stdout.String() != "apiVersion: 1\n" {
		t.Fatalf("unexpected get output %q", stdout.String())
	}
}

func TestMissingCredentialIsExplained(t *testing.T) {
	_, endpoint := startFake(t)
	t.Setenv("CONFIG_CENTER_SERVICE_TOKEN", "")
	file := writeTempValue(t, "jaeger.yaml", "apiVersion: 1\n")

	var stdout bytes.Buffer
	err := run(context.Background(), []string{
		"put", "-endpoint", endpoint,
		"-namespace", "observability", "-environment", "prod",
		"-key", "grafana/datasources/jaeger", "-file", file,
	}, &stdout, &stdout)
	if err == nil || !strings.Contains(err.Error(), "CONFIG_CENTER_SERVICE_TOKEN") {
		t.Fatalf("expected a credential hint, got %v", err)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"rm"}, &stdout, &stderr); err == nil {
		t.Fatal("expected an unknown subcommand error")
	}
	if !strings.Contains(stderr.String(), "configctl") {
		t.Fatalf("usage should go to stderr, got %q", stderr.String())
	}
}
