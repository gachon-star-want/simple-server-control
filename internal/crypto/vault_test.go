package crypto

import (
	"path/filepath"
	"testing"
)

func TestVaultRoundTripAndPersistence(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")

	v1, err := LoadOrCreateVault(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateVault create: %v", err)
	}

	secret := "postgres://user:sup3r-s3cr3t@postgres:5432/pleumrouter"
	enc, err := v1.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(enc) == secret {
		t.Fatal("ciphertext must not equal plaintext")
	}

	v2, err := LoadOrCreateVault(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateVault reload: %v", err)
	}

	dec, err := v2.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != secret {
		t.Fatalf("got %q, want %q", dec, secret)
	}

	// Tampered ciphertext must fail authentication.
	enc[len(enc)-1] ^= 0xFF
	if _, err := v2.Decrypt(enc); err == nil {
		t.Fatal("expected authentication error for tampered ciphertext")
	}
}
