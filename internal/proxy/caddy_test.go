package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/proxy"
)

func TestCaddyMultiDomainSyncAndSkipUnverifiedHTTPS(t *testing.T) {
	vault, _ := crypto.NewVaultFromKey(bytes.Repeat([]byte{0x22}, 32))
	store, err := db.Open(filepath.Join(t.TempDir(), "caddy.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	p1, env1, _ := store.CreateProject("Pleum", "")
	p2, env2, _ := store.CreateProject("Blog", "")

	s1, _ := store.CreateService(db.CreateServiceInput{ProjectID: p1.ID, EnvironmentID: env1.ID, Name: "api", Kind: "app", Port: 4000})
	s2, _ := store.CreateService(db.CreateServiceInput{ProjectID: p2.ID, EnvironmentID: env2.ID, Name: "web", Kind: "app", Port: 3000})

	_, _ = store.UpdateServiceRuntimeStatus(s1.ID, "running", "c1", "ssc-pleum-api-1", "img1", 10, false)
	_, _ = store.UpdateServiceRuntimeStatus(s2.ID, "idle", "", "", "", 10, false)

	_, _ = store.CreateDomain(s1.ID, "api.pleum.com", false, true, true)
	_, _ = store.CreateDomain(s2.ID, "pending.blog.com", false, true, false)

	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/load" || r.Method != http.MethodPost {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode caddy load payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	router := proxy.NewCaddyRouter(store, nil, srv.URL, "ssc-ingress", t.TempDir(), ":8080", "example.com")
	if err := router.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	admin := received["admin"].(map[string]any)
	if admin["listen"] != "0.0.0.0:2019" {
		t.Fatalf("expected caddy admin listener 0.0.0.0:2019, got %v", admin["listen"])
	}

	httpApp := received["apps"].(map[string]any)["http"].(map[string]any)
	mainSrv := httpApp["servers"].(map[string]any)["main"].(map[string]any)
	routes := mainSrv["routes"].([]any)
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes (panel + 2 domains), got %d", len(routes))
	}

	autoHTTPS := mainSrv["automatic_https"].(map[string]any)
	skipList := autoHTTPS["skip"].([]any)
	if !reflect.DeepEqual(skipList, []any{"pending.blog.com"}) {
		t.Fatalf("expected unverified domain pending.blog.com skipped from ACME, got %v", skipList)
	}

	if auto := router.AutoDomain("api", "pleum"); auto != "api-pleum.example.com" {
		t.Fatalf("unexpected auto domain: %q", auto)
	}
}
