package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Config holds runtime paths and server configuration for simple-server-control.
type Config struct {
	Version        string
	Repo           string
	DataDir        string
	ListenAddr     string
	CaddyAdminURL  string
	BuildkitAddr   string
	IngressNetwork string
	BaseDomain     string
	MasterKeyPath  string
	SetupTokenPath string
	PanelSocket    string // Unix socket Caddy uses to reach the panel (see proxy.PanelSocketName)
	SetupToken     string
	JWTSecret      string
	PublicOrigin   string
}

// Load builds Config from environment variables with sensible Ubuntu / local-dev defaults.
func Load() (*Config, error) {
	dataDir := envOr("SSC_DATA_DIR", defaultDataDir())
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	for _, sub := range []string{"logs", "repos", "caddy/data", "caddy/config"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o750); err != nil {
			return nil, err
		}
	}

	cfg := &Config{
		Version:        "0.1.0-dev",
		Repo:           envOr("SSC_REPO", "gachon-star-want/simple-server-control"),
		DataDir:        dataDir,
		// Loopback only: the panel is reached via SSH tunnel or the HTTPS panel domain through Caddy.
		ListenAddr:     envOr("SSC_LISTEN_ADDR", "127.0.0.1:8080"),
		CaddyAdminURL:  strings.TrimRight(envOr("SSC_CADDY_ADMIN_URL", "http://127.0.0.1:2019"), "/"),
		BuildkitAddr:   envOr("SSC_BUILDKIT_HOST", "docker-container://ssc-buildkit"),
		IngressNetwork: envOr("SSC_INGRESS_NETWORK", "ssc-ingress"),
		BaseDomain:     strings.TrimSpace(os.Getenv("SSC_BASE_DOMAIN")),
		MasterKeyPath:  filepath.Join(dataDir, "master.key"),
		SetupTokenPath: filepath.Join(dataDir, "setup.token"),
		PanelSocket:    filepath.Join(dataDir, "caddy", "config", "panel.sock"),
		PublicOrigin:   strings.TrimRight(strings.TrimSpace(os.Getenv("SSC_PUBLIC_ORIGIN")), "/"),
	}

	jwtSecret, err := ensureHexSecret(filepath.Join(dataDir, "jwt.secret"), 32)
	if err != nil {
		return nil, err
	}
	cfg.JWTSecret = jwtSecret

	setupToken, err := ensureHexSecret(cfg.SetupTokenPath, 24)
	if err != nil {
		return nil, err
	}
	cfg.SetupToken = setupToken

	return cfg, nil
}

func defaultDataDir() string {
	if _, err := os.Stat("/var/lib/ssc"); err == nil {
		return "/var/lib/ssc"
	}
	if _, err := os.Stat("/var/lib/simple-server-control"); err == nil {
		return "/var/lib/simple-server-control"
	}
	if os.Geteuid() == 0 {
		return "/var/lib/ssc"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "./.ssc-data"
	}
	return filepath.Join(home, ".simple-server-control")
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func ensureHexSecret(path string, byteLen int) (string, error) {
	if raw, err := os.ReadFile(path); err == nil {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed != "" {
			return trimmed, nil
		}
	}
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", err
	}
	return secret, nil
}
