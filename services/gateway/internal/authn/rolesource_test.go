package authn

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRoleSourceFetchAndCache(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/get-user" || r.URL.Query().Get("id") != "lens/alice" {
			t.Errorf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"status":"ok","data":{"name":"alice","roles":[{"owner":"lens","name":"consumer"},{"owner":"lens","name":"vip"}]}}`)
	}))
	defer srv.Close()

	s := NewCasdoorRoleSource(srv.URL, time.Minute)
	roles, err := s.Roles(context.Background(), "lens", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 || roles[0] != "consumer" || roles[1] != "vip" {
		t.Fatalf("roles=%v", roles)
	}
	// 缓存命中：不再回源。
	if _, err := s.Roles(context.Background(), "lens", "alice"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1 (cache)", hits.Load())
	}
}

func TestRoleSourceNegativeCache(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"status":"ok","data":{"roles":[]}}`)
	}))
	defer srv.Close()

	s := NewCasdoorRoleSource(srv.URL, time.Minute)
	for i := 0; i < 3; i++ {
		roles, err := s.Roles(context.Background(), "lens", "norole")
		if err != nil || len(roles) != 0 {
			t.Fatalf("roles=%v err=%v", roles, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d want 1 (negative cache)", hits.Load())
	}
}

func TestRoleSourceStaleOnError(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"status":"ok","data":{"roles":[{"name":"consumer"}]}}`)
	}))
	defer srv.Close()

	s := NewCasdoorRoleSource(srv.URL, time.Nanosecond) // 立即过期,强制回源
	if _, err := s.Roles(context.Background(), "lens", "alice"); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	time.Sleep(time.Millisecond)
	roles, err := s.Roles(context.Background(), "lens", "alice")
	if err != nil {
		t.Fatalf("stale fallback must serve: %v", err)
	}
	if len(roles) != 1 || roles[0] != "consumer" {
		t.Fatalf("roles=%v", roles)
	}
	if s.StaleServedCount() == 0 {
		t.Fatal("stale counter must increment")
	}
}

func TestRoleSourceErrorNoCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	s := NewCasdoorRoleSource(srv.URL, time.Minute)
	if _, err := s.Roles(context.Background(), "lens", "ghost"); err == nil {
		t.Fatal("no cache + fetch error must return error")
	}
}
