package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Vault provides AES-256-GCM encryption and decryption for persisted secrets and keys.
type Vault struct {
	aead cipher.AEAD
}

// NewVaultFromKey creates a Vault using an explicit 32-byte key.
func NewVaultFromKey(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// LoadOrCreateVault loads a hex-encoded 32-byte master key from path or generates a new one.
func LoadOrCreateVault(keyPath string) (*Vault, error) {
	if envKey := strings.TrimSpace(os.Getenv("SSC_MASTER_KEY")); envKey != "" {
		decoded, err := hex.DecodeString(envKey)
		if err != nil {
			return nil, fmt.Errorf("invalid SSC_MASTER_KEY hex: %w", err)
		}
		return NewVaultFromKey(decoded)
	}

	if raw, err := os.ReadFile(keyPath); err == nil {
		decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("invalid master key file: %w", err)
		}
		return NewVaultFromKey(decoded)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return NewVaultFromKey(key)
}

// Encrypt encrypts plaintext into nonce || ciphertext.
func (v *Vault) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return v.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt decrypts nonce || ciphertext back to plaintext.
func (v *Vault) Decrypt(ciphertext []byte) (string, error) {
	nonceSize := v.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, body := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plain, err := v.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// RandomHex generates a cryptographically random hex string of nBytes length.
func RandomHex(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
