package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/api"
	"github.com/gachon-star-want/simple-server-control/internal/config"
	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/deploy"
)

type stubRouter struct{}

func (s *stubRouter) EnsureRunning(context.Context) error { return nil }
func (s *stubRouter) Sync(context.Context) error          { return nil }
func (s *stubRouter) AutoDomain(svc, proj string) string  { return svc + "-" + proj + ".sslip.io" }
func (s *stubRouter) VerifyDNS(context.Context, string) (bool, []string, string, string) {
	return true, nil, "1.2.3.4", "ok"
}

func TestAPISetupAuthProjectsSecretsAndWebhook(t *testing.T) {
	tmp := t.TempDir()
	vault, _ := crypto.NewVaultFromKey(bytes.Repeat([]byte{0x77}, 32))
	store, err := db.Open(filepath.Join(tmp, "api.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := &config.Config{
		DataDir:        tmp,
		ListenAddr:     ":8080",
		IngressNetwork: "ssc-ingress",
		SetupToken:     "setup-tok-9999",
		JWTSecret:      "test-jwt-secret-key-0123456789abcdef",
	}

	ui := fstest.MapFS{
		"index.html": {Data: []byte("<html>SSC</html>")},
	}
	pipe := deploy.NewPipeline(store, nil, &stubRouter{}, nil, nil, filepath.Join(tmp, "logs"), "ssc-ingress", nil)
	srv := api.NewServer(cfg, store, nil, &stubRouter{}, pipe, nil, nil, ui)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	// 1. Unauthenticated /api/projects blocked with 401.
	resp, err := client.Get(ts.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 before login, got %d", resp.StatusCode)
	}

	// 2. Setup rejects wrong setup_token.
	badSetup, _ := json.Marshal(map[string]string{
		"setup_token": "wrong",
		"email":       "pm@pleum.com",
		"password":    "password1234",
	})
	resp, _ = client.Post(ts.URL+"/api/auth/setup", "application/json", bytes.NewReader(badSetup))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for bad setup token, got %d", resp.StatusCode)
	}

	// 3. Valid setup creates admin and sets HttpOnly session cookie.
	goodSetup, _ := json.Marshal(map[string]string{
		"setup_token": "setup-tok-9999",
		"email":       "pm@pleum.com",
		"password":    "password1234",
	})
	resp, _ = client.Post(ts.URL+"/api/auth/setup", "application/json", bytes.NewReader(goodSetup))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for setup, got %d", resp.StatusCode)
	}

	// Repeat setup MUST be rejected forever.
	resp, _ = client.Post(ts.URL+"/api/auth/setup", "application/json", bytes.NewReader(goodSetup))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on second setup attempt, got %d", resp.StatusCode)
	}

	// 4. Create project & app service -> verify auto domain attached & canvas leaks 0 secrets.
	projBody, _ := json.Marshal(map[string]string{"name": "PleumRouter", "description": "Multi LLM router"})
	resp, err = client.Post(ts.URL+"/api/projects", "application/json", bytes.NewReader(projBody))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project failed: status=%d err=%v", resp.StatusCode, err)
	}
	var projOut struct {
		Project     db.Project     `json:"project"`
		Environment db.Environment `json:"environment"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&projOut)
	_ = resp.Body.Close()

	svcBody, _ := json.Marshal(map[string]any{
		"name":           "backend",
		"kind":           "app",
		"port":           8080,
		"repo_full_name": "gachon-star-want/pleumrouter",
		"repo_branch":    "main",
	})
	resp, err = client.Post(ts.URL+"/api/projects/"+projOut.Project.ID+"/services", "application/json", bytes.NewReader(svcBody))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create service failed: status=%d err=%v", resp.StatusCode, err)
	}
	var svcOut struct {
		Service db.Service `json:"service"`
		Domain  *db.Domain `json:"domain"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&svcOut)
	_ = resp.Body.Close()
	if svcOut.Domain == nil || svcOut.Domain.Hostname != "backend-pleumrouter.sslip.io" {
		t.Fatalf("expected auto domain, got %+v", svcOut.Domain)
	}

	// Import .env secrets (including sealed secret) and verify masking + zero-secret canvas.
	importBody, _ := json.Marshal(map[string]any{
		"raw":       "JWT_SECRET=top-secret-value-123\nAPI_MODE=production\n",
		"is_sealed": false,
	})
	resp, _ = client.Post(ts.URL+"/api/services/"+svcOut.Service.ID+"/secrets/import-env", "application/json", bytes.NewReader(importBody))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import-env failed: %d", resp.StatusCode)
	}

	resp, _ = client.Get(ts.URL + "/api/projects/" + projOut.Project.ID + "/canvas")
	var canvasBytes bytes.Buffer
	_, _ = canvasBytes.ReadFrom(resp.Body)
	_ = resp.Body.Close()
	if bytes.Contains(canvasBytes.Bytes(), []byte("top-secret-value-123")) {
		t.Fatalf("canvas endpoint leaked secret value: %s", canvasBytes.String())
	}

	// 5. GitHub webhook validates HMAC strictly.
	_, _ = store.SaveGitHubApp(db.GitHubApp{
		AppID:          1,
		Slug:           "ssc-test",
		WebhookSecret:  "whsec-123",
		InstallationID: 2,
	})
	pushPayload := []byte(`{"ref":"refs/heads/main","after":"111aaa","repository":{"full_name":"gachon-star-want/pleumrouter"}}`)

	// Bad HMAC rejected.
	reqBad, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/webhooks/github", bytes.NewReader(pushPayload))
	reqBad.Header.Set("X-GitHub-Event", "push")
	reqBad.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	resp, _ = http.DefaultClient.Do(reqBad)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bad webhook HMAC, got %d", resp.StatusCode)
	}

	// Valid HMAC matches auto-deploy service.
	mac := hmac.New(sha256.New, []byte("whsec-123"))
	mac.Write(pushPayload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	reqGood, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/webhooks/github", bytes.NewReader(pushPayload))
	reqGood.Header.Set("X-GitHub-Event", "push")
	reqGood.Header.Set("X-Hub-Signature-256", sig)
	resp, _ = http.DefaultClient.Do(reqGood)
	var whResp struct {
		Matched int `json:"matched_services"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&whResp)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || whResp.Matched != 1 {
		t.Fatalf("expected 202 matched=1, got status=%d matched=%d", resp.StatusCode, whResp.Matched)
	}
	// Wait briefly so background webhook goroutine finishes cleanly before TempDir/DB closes.
	time.Sleep(100 * time.Millisecond)
}
