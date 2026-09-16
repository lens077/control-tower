package configadmin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	configv1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/api/config/v1/configv1connect"
	"github.com/lens077/control-tower/constants"
	"github.com/lens077/control-tower/sdk/configadmin"
)

// fakeService 是一个内存 Config Center：记录收到的请求头与 PutKey 参数，
// 让测试既能断言线上行为（凭据怎么贴）又能断言语义（新建 vs 更新）。
type fakeService struct {
	configv1connect.UnimplementedConfigServiceHandler

	mu      sync.Mutex
	entries map[string]*configv1.ConfigEntry
	headers []http.Header
	putErr  error
}

func newFakeService() *fakeService {
	return &fakeService{entries: map[string]*configv1.ConfigEntry{}}
}

func (f *fakeService) record(header http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.headers = append(f.headers, header.Clone())
}

func (f *fakeService) lastHeader() http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.headers) == 0 {
		return http.Header{}
	}
	return f.headers[len(f.headers)-1]
}

func key(namespace, environment, name string) string {
	return namespace + "\x00" + environment + "\x00" + name
}

func (f *fakeService) GetKey(_ context.Context, request *connect.Request[configv1.GetKeyRequest]) (*connect.Response[configv1.GetKeyResponse], error) {
	f.record(request.Header())
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[key(request.Msg.GetNamespace(), request.Msg.GetEnvironment(), request.Msg.GetKey())]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("key not found"))
	}
	return connect.NewResponse(&configv1.GetKeyResponse{Entry: entry}), nil
}

func (f *fakeService) PutKey(_ context.Context, request *connect.Request[configv1.PutKeyRequest]) (*connect.Response[configv1.PutKeyResponse], error) {
	f.record(request.Header())
	if f.putErr != nil {
		return nil, f.putErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := key(request.Msg.GetNamespace(), request.Msg.GetEnvironment(), request.Msg.GetKey())
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
		IsSecret:    request.Msg.GetIsSecret(),
		Description: request.Msg.GetDescription(),
	}
	f.entries[id] = entry
	return connect.NewResponse(&configv1.PutKeyResponse{Entry: entry}), nil
}

func (f *fakeService) ListKeys(_ context.Context, request *connect.Request[configv1.ListKeysRequest]) (*connect.Response[configv1.ListKeysResponse], error) {
	f.record(request.Header())
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

func (f *fakeService) ListNamespaces(_ context.Context, request *connect.Request[configv1.ListNamespacesRequest]) (*connect.Response[configv1.ListNamespacesResponse], error) {
	f.record(request.Header())
	return connect.NewResponse(&configv1.ListNamespacesResponse{
		Namespaces: []*configv1.NamespaceInfo{{Namespace: "observability", Environments: []string{"dev", "prod"}}},
	}), nil
}

func startFake(t *testing.T) (*fakeService, string) {
	t.Helper()
	service := newFakeService()
	mux := http.NewServeMux()
	mux.Handle(configv1connect.NewConfigServiceHandler(service))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return service, server.URL
}

func TestNewRejectsMissingCredential(t *testing.T) {
	if _, err := configadmin.New("https://config-api.example", configadmin.Credential{}); err == nil {
		t.Fatal("expected a credential requirement error")
	}
}

func TestNewRejectsSchemelessEndpoint(t *testing.T) {
	if _, err := configadmin.New("config-api.example", configadmin.Credential{MachineToken: "ct_x"}); err == nil {
		t.Fatal("expected a scheme requirement error")
	}
}

func TestPutThenGetRoundTrip(t *testing.T) {
	service, endpoint := startFake(t)
	client, err := configadmin.New(endpoint, configadmin.Credential{MachineToken: "ct_operator"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	selector := configadmin.Selector{Namespace: "observability", Environment: "prod", Key: "grafana/datasources/jaeger"}

	if _, err := client.Get(context.Background(), selector); !errors.Is(err, configadmin.ErrNotFound) {
		t.Fatalf("expected ErrNotFound before the first write, got %v", err)
	}

	entry, err := client.Put(context.Background(), configadmin.PutRequest{
		Selector: selector,
		Format:   configv1.ConfigFormat_CONFIG_FORMAT_YAML,
		Value:    "apiVersion: 1\n",
		Comment:  "first",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if entry.Version != 1 {
		t.Fatalf("expected version 1, got %d", entry.Version)
	}

	got, err := client.Get(context.Background(), selector)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Value != "apiVersion: 1\n" || got.Format != configv1.ConfigFormat_CONFIG_FORMAT_YAML {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// 第二次写必须落成新版本，CLI 的「created vs updated」就靠这个区分。
	again, err := client.Put(context.Background(), configadmin.PutRequest{
		Selector: selector,
		Format:   configv1.ConfigFormat_CONFIG_FORMAT_YAML,
		Value:    "apiVersion: 2\n",
	})
	if err != nil {
		t.Fatalf("second put: %v", err)
	}
	if again.Version != 2 {
		t.Fatalf("expected version 2, got %d", again.Version)
	}

	header := service.lastHeader()
	if header.Get(constants.ServiceTokenHeader) != "ct_operator" {
		t.Fatalf("machine token header missing: %q", header.Get(constants.ServiceTokenHeader))
	}
	if header.Get("Authorization") != "" {
		t.Fatal("machine token requests must not also send Authorization")
	}
	if header.Get(constants.ClientNameHeader) == "" {
		t.Fatal("client name header missing")
	}
}

func TestBearerCredentialUsesAuthorizationHeader(t *testing.T) {
	service, endpoint := startFake(t)
	client, err := configadmin.New(endpoint, configadmin.Credential{BearerToken: "jwt-token"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.ListNamespaces(context.Background()); err != nil {
		t.Fatalf("list namespaces: %v", err)
	}
	header := service.lastHeader()
	if header.Get("Authorization") != "Bearer jwt-token" {
		t.Fatalf("unexpected Authorization header %q", header.Get("Authorization"))
	}
	if header.Get(constants.ServiceTokenHeader) != "" {
		t.Fatal("bearer requests must not send a service token header")
	}
}

func TestPutRejectsEmptyValue(t *testing.T) {
	_, endpoint := startFake(t)
	client, err := configadmin.New(endpoint, configadmin.Credential{MachineToken: "ct_operator"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.Put(context.Background(), configadmin.PutRequest{
		Selector: configadmin.Selector{Namespace: "n", Environment: "e", Key: "k"},
		Format:   configv1.ConfigFormat_CONFIG_FORMAT_YAML,
	})
	if err == nil {
		t.Fatal("expected an empty-value rejection")
	}
}

func TestPutSurfacesPermissionDenied(t *testing.T) {
	service, endpoint := startFake(t)
	service.putErr = connect.NewError(connect.CodePermissionDenied, errors.New("scope does not allow prod"))
	client, err := configadmin.New(endpoint, configadmin.Credential{MachineToken: "ct_pre_only"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.Put(context.Background(), configadmin.PutRequest{
		Selector: configadmin.Selector{Namespace: "observability", Environment: "prod", Key: "k"},
		Format:   configv1.ConfigFormat_CONFIG_FORMAT_YAML,
		Value:    "a: 1\n",
	})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected permission denied to survive wrapping, got %v", err)
	}
}

func TestFormatHelpers(t *testing.T) {
	if format, err := configadmin.ParseFormat("YML"); err != nil || format != configv1.ConfigFormat_CONFIG_FORMAT_YAML {
		t.Fatalf("ParseFormat(YML) = %v, %v", format, err)
	}
	if _, err := configadmin.ParseFormat("ini"); err == nil {
		t.Fatal("expected unknown format rejection")
	}
	if format, ok := configadmin.FormatForPath("examples/grafana-datasources/jaeger.yaml"); !ok || format != configv1.ConfigFormat_CONFIG_FORMAT_YAML {
		t.Fatalf("FormatForPath yaml = %v, %v", format, ok)
	}
	if _, ok := configadmin.FormatForPath("routes.conf"); ok {
		t.Fatal("unknown extension must not be inferred")
	}
	if configadmin.FormatName(configv1.ConfigFormat_CONFIG_FORMAT_JSON) != "json" {
		t.Fatal("FormatName json mismatch")
	}
}
