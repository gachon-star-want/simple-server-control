package github_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/github"
)

func generateTestRSA(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	}))
}

func TestManifestExchangeInstallationVerifyAndRepos(t *testing.T) {
	vault, _ := crypto.NewVaultFromKey(bytes.Repeat([]byte{0x33}, 32))
	store, err := db.Open(filepath.Join(t.TempDir(), "gh.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	privPEM := generateTestRSA(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app-manifests/code-123/conversions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             9988,
				"slug":           "ssc-test-app",
				"name":           "SSC Test App",
				"html_url":       "https://github.com/apps/ssc-test-app",
				"client_id":      "cid",
				"client_secret":  "csec",
				"webhook_secret": "whsec",
				"pem":            privPEM,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/app/installations/777":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				http.Error(w, "missing app jwt", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     777,
				"app_id": 9988,
				"account": map[string]any{
					"login": "gachon-star-want",
					"type":  "User",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/app/installations/999":
			http.Error(w, "not found", http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/777/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "ghs_test_install_tok",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/installation/repositories":
			if r.Header.Get("Authorization") != "token ghs_test_install_tok" {
				http.Error(w, "bad token", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"repositories": []map[string]any{
					{"id": 1, "full_name": "gachon-star-want/pleumrouter", "default_branch": "main", "private": true},
				},
			})
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := github.NewClient(store, srv.URL)
	state, err := client.CreateState("manifest")
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, err := github.BuildManifest("my-ssc", "https://ssc.example.com", state)
	if err != nil || !strings.Contains(manifestJSON, "/api/webhooks/github") {
		t.Fatalf("unexpected manifest: %s err=%v", manifestJSON, err)
	}
	if err := client.ConsumeState(state, "manifest"); err != nil {
		t.Fatalf("ConsumeState: %v", err)
	}
	if err := client.ConsumeState(state, "manifest"); err == nil {
		t.Fatalf("state replay should fail")
	}

	app, err := client.ExchangeManifestCode(context.Background(), "code-123")
	if err != nil {
		t.Fatalf("ExchangeManifestCode: %v", err)
	}

	// Spoofed installation ID MUST fail cross-verification.
	if _, err := client.VerifyAndSaveInstallation(context.Background(), app.ID, 999); err == nil {
		t.Fatalf("expected unverified installation 999 to fail")
	}

	// Valid installation ID passes JWT cross-verification.
	verifiedApp, err := client.VerifyAndSaveInstallation(context.Background(), app.ID, 777)
	if err != nil {
		t.Fatalf("VerifyAndSaveInstallation: %v", err)
	}
	if !verifiedApp.Connected || verifiedApp.AccountLogin != "gachon-star-want" {
		t.Fatalf("expected verified connected app, got %+v", verifiedApp)
	}

	repos, err := client.ListRepositories(context.Background(), verifiedApp)
	if err != nil || len(repos) != 1 || repos[0].FullName != "gachon-star-want/pleumrouter" {
		t.Fatalf("unexpected repos: %+v err=%v", repos, err)
	}
}

func TestWebhookVerifyAndParsePush(t *testing.T) {
	payload := []byte(`{"ref":"refs/heads/main","after":"abc1234","deleted":false,"repository":{"full_name":"gachon-star-want/pleumrouter"},"head_commit":{"id":"abc1234","message":"feat: update router\n\nbody"}}`)
	mac := hmac.New(sha256.New, []byte("secret-xyz"))
	mac.Write(payload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !github.VerifySignature("secret-xyz", payload, sig) {
		t.Fatalf("expected valid HMAC signature")
	}
	if github.VerifySignature("wrong", payload, sig) {
		t.Fatalf("expected invalid secret rejection")
	}
	evt, err := github.ParsePushEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	if evt.Branch != "main" || evt.CommitSHA != "abc1234" || evt.CommitMessage != "feat: update router" {
		t.Fatalf("unexpected parsed push: %+v", evt)
	}
}
