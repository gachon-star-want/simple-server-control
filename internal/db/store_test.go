package db_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
)

func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	vault, err := crypto.NewVaultFromKey(key)
	if err != nil {
		t.Fatalf("NewVaultFromKey: %v", err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"), vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestProjectsAndZeroSecretCanvas(t *testing.T) {
	store := openTestStore(t)

	p1, env1, err := store.CreateProject("Pleum Router", "AI 라우터")
	if err != nil {
		t.Fatalf("CreateProject 1: %v", err)
	}
	if p1.NetworkName != "ssc-proj-pleum-router" {
		t.Fatalf("unexpected network name: %q", p1.NetworkName)
	}

	p2, _, err := store.CreateProject("플름 두번째", "")
	if err != nil {
		t.Fatalf("CreateProject Korean: %v", err)
	}
	if !strings.HasPrefix(p2.NetworkName, "ssc-proj-project-") {
		t.Fatalf("expected safe slug prefix for non-ASCII name, got %q", p2.NetworkName)
	}

	pg, err := store.CreateService(db.CreateServiceInput{
		ProjectID:     p1.ID,
		EnvironmentID: env1.ID,
		Name:          "postgres",
		Kind:          "database",
		DBType:        "postgres",
		DBImage:       "postgres:16-alpine",
	})
	if err != nil {
		t.Fatalf("CreateService db: %v", err)
	}

	app, err := store.CreateService(db.CreateServiceInput{
		ProjectID:     p1.ID,
		EnvironmentID: env1.ID,
		Name:          "api",
		Kind:          "app",
		Port:          8080,
		RepoFullName:  "gachon-star-want/pleumrouter",
		RepoBranch:    "main",
	})
	if err != nil {
		t.Fatalf("CreateService app: %v", err)
	}

	const superSecretDBPass = "ultra-secret-postgres-pw-9911"
	const sealedAPIKey = "sk-live-never-reveal-12345"

	if err := store.UpsertSecret(env1.ID, pg.ID, db.SecretInput{
		Key:      "DATABASE_URL",
		Value:    "postgres://app:" + superSecretDBPass + "@postgres:5432/app",
		IsSystem: true,
	}); err != nil {
		t.Fatalf("UpsertSecret pg: %v", err)
	}

	if err := store.UpsertSecret(env1.ID, "", db.SecretInput{
		Key:   "SHARED_FLAG",
		Value: "prod-mode",
	}); err != nil {
		t.Fatalf("UpsertSecret shared: %v", err)
	}

	if err := store.UpsertSecret(env1.ID, app.ID, db.SecretInput{
		Key:      "OPENAI_API_KEY",
		Value:    sealedAPIKey,
		IsSealed: true,
	}); err != nil {
		t.Fatalf("UpsertSecret app sealed: %v", err)
	}

	if _, err := store.CreateServiceLink(p1.ID, env1.ID, app.ID, pg.ID, "DATABASE_URL"); err != nil {
		t.Fatalf("CreateServiceLink: %v", err)
	}

	// Verify masked list hides values and reveal=true still protects sealed keys.
	revealed, err := store.ListSecrets(env1.ID, app.ID, true)
	if err != nil {
		t.Fatalf("ListSecrets reveal: %v", err)
	}
	if len(revealed) != 1 || revealed[0].Value == sealedAPIKey || !revealed[0].Masked {
		t.Fatalf("sealed secret leaked or unmasked: %+v", revealed)
	}

	// Verify runtime env resolves shared + link + sealed value cleanly.
	runtimeEnv, err := store.ResolveServiceRuntimeEnv(app.ID)
	if err != nil {
		t.Fatalf("ResolveServiceRuntimeEnv: %v", err)
	}
	if runtimeEnv["OPENAI_API_KEY"] != sealedAPIKey {
		t.Fatalf("expected runtime OPENAI_API_KEY, got %q", runtimeEnv["OPENAI_API_KEY"])
	}
	if runtimeEnv["SHARED_FLAG"] != "prod-mode" {
		t.Fatalf("expected shared flag, got %q", runtimeEnv["SHARED_FLAG"])
	}
	if !strings.Contains(runtimeEnv["DATABASE_URL"], superSecretDBPass) {
		t.Fatalf("expected resolved DATABASE_URL via link, got %q", runtimeEnv["DATABASE_URL"])
	}

	// Audit zero-secret Canvas JSON output.
	canvas, err := store.GetProjectCanvas(p1.ID, env1.ID)
	if err != nil {
		t.Fatalf("GetProjectCanvas: %v", err)
	}
	rawJSON, err := json.Marshal(canvas)
	if err != nil {
		t.Fatalf("marshal canvas: %v", err)
	}
	if bytes.Contains(rawJSON, []byte(superSecretDBPass)) || bytes.Contains(rawJSON, []byte(sealedAPIKey)) || bytes.Contains(rawJSON, []byte("prod-mode")) {
		t.Fatalf("canvas JSON leaked secret material: %s", rawJSON)
	}
}

func TestOutOfOrderStatusGuardAndCrossEnvIsolation(t *testing.T) {
	store := openTestStore(t)
	p1, env1, err := store.CreateProject("App One", "")
	if err != nil {
		t.Fatal(err)
	}
	env2, err := store.CreateEnvironment(p1.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}

	s1, err := store.CreateService(db.CreateServiceInput{
		ProjectID:     p1.ID,
		EnvironmentID: env1.ID,
		Name:          "web",
		Kind:          "app",
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.CreateService(db.CreateServiceInput{
		ProjectID:     p1.ID,
		EnvironmentID: env2.ID,
		Name:          "redis",
		Kind:          "database",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Cross-environment link MUST fail.
	if _, err := store.CreateServiceLink(p1.ID, env1.ID, s1.ID, s2.ID, "REDIS_URL"); err == nil {
		t.Fatalf("expected cross-environment link to fail")
	}

	// Out-of-order timestamp guard.
	updated, err := store.UpdateServiceRuntimeStatus(s1.ID, "running", "cid-new", "ssc-app-new", "img:2", 200, false)
	if err != nil || !updated {
		t.Fatalf("expected newer timestamp update to apply, updated=%v err=%v", updated, err)
	}
	staleUpdated, err := store.UpdateServiceRuntimeStatus(s1.ID, "stopped", "cid-old", "ssc-app-old", "img:1", 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if staleUpdated {
		t.Fatalf("expected stale observed_at=100 update to be ignored")
	}
	got, err := store.GetService(s1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.ActiveContainerID != "cid-new" {
		t.Fatalf("stale update mutated service: %+v", got)
	}
}
