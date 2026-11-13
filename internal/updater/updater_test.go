package updater_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gachon-star-want/simple-server-control/internal/updater"
)

func TestSemVerComparisonAndDevGuard(t *testing.T) {
	if !updater.IsNewerVersion("v0.1.1", "v0.2.0", false) {
		t.Fatalf("expected v0.2.0 > v0.1.1")
	}
	if updater.IsNewerVersion("v0.2.0", "v0.1.1", false) {
		t.Fatalf("must never downgrade v0.2.0 -> v0.1.1")
	}
	if updater.IsNewerVersion("v0.2.0", "v0.2.0", false) {
		t.Fatalf("same version must not trigger update")
	}
	if updater.IsNewerVersion("0.1.0-dev", "v0.2.0", false) {
		t.Fatalf("-dev build must be skipped unless forced")
	}
	if !updater.IsNewerVersion("0.1.0-dev", "v0.2.0", true) {
		t.Fatalf("expected forced -dev upgrade to proceed")
	}
}

func TestUpgradeVerifiesChecksumDeferAndAtomicSwap(t *testing.T) {
	newBin := []byte("#!/bin/sh\necho 'simple-server-control (ssc) v0.2.0'\n")
	sum := sha256.Sum256(newBin)
	goodChecksum := fmt.Sprintf("%s  simple-server-control-linux-amd64\n", hex.EncodeToString(sum[:]))
	badChecksum := fmt.Sprintf("%064x  simple-server-control-linux-amd64\n", 1)

	serveBadChecksum := true
	mux := http.NewServeMux()
	mux.HandleFunc("/org/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://github.com/org/repo/releases/tag/v0.2.0")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/org/repo/releases/download/v0.2.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		if serveBadChecksum {
			_, _ = w.Write([]byte(badChecksum))
			return
		}
		_, _ = w.Write([]byte(goodChecksum))
	})
	mux.HandleFunc("/org/repo/releases/download/v0.2.0/simple-server-control-linux-amd64", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newBin)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "ssc")
	oldBytes := []byte("old-binary-v0.1.1")
	if err := os.WriteFile(target, oldBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Active deployment defers upgrade without touching target binary.
	busyClient := updater.New(updater.Options{
		Repo:             "org/repo",
		CurrentVersion:   "v0.1.1",
		TargetPath:       target,
		OS:               "linux",
		Arch:             "amd64",
		AutoMode:         true,
		GitHubWebBaseURL: ts.URL,
		ActiveDeployFn:   func(context.Context) (int64, error) { return 1, nil },
		VerifyBinary:     func(context.Context, string) error { return nil },
	})
	res, err := busyClient.ApplyUpgrade(context.Background())
	if err != nil {
		t.Fatalf("unexpected error while busy: %v", err)
	}
	if res.Updated {
		t.Fatalf("expected upgrade deferral during active deployment")
	}

	// 2. Bad SHA-256 checksum fails and preserves original binary untouched.
	badClient := updater.New(updater.Options{
		Repo:             "org/repo",
		CurrentVersion:   "v0.1.1",
		TargetPath:       target,
		OS:               "linux",
		Arch:             "amd64",
		GitHubWebBaseURL: ts.URL,
		VerifyBinary:     func(context.Context, string) error { return nil },
	})
	if _, err := badClient.ApplyUpgrade(context.Background()); err == nil {
		t.Fatalf("expected checksum mismatch error")
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(oldBytes) {
		t.Fatalf("original binary modified on checksum failure")
	}

	// 3. Valid checksum + passing verify check replaces binary cleanly.
	serveBadChecksum = false
	res, err = badClient.ApplyUpgrade(context.Background())
	if err != nil {
		t.Fatalf("expected clean upgrade, got: %v", err)
	}
	if !res.Updated || res.NewVersion != "v0.2.0" || !res.RestartRequired {
		t.Fatalf("unexpected upgrade result: %+v", res)
	}
	got, _ = os.ReadFile(target)
	if string(got) != string(newBin) {
		t.Fatalf("target binary not swapped cleanly")
	}
}
