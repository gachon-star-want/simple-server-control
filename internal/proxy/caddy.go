// Package proxy manages multi-domain HTTPS reverse proxy configuration via Caddy Admin API.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/docker"
)

const (
	// CaddyContainerName is the single reverse proxy container running on host ports 80/443.
	CaddyContainerName = "ssc-caddy"
	// CaddyImage is the lightweight upstream Alpine Caddy image.
	CaddyImage = "caddy:2-alpine"
	// PanelSocketName is the Unix socket ssc serves the panel on for Caddy. It lives in
	// <data>/caddy/config, which is already bind-mounted at /config in the Caddy container,
	// so the panel needs no network listener reachable from containers or the internet.
	PanelSocketName = "panel.sock"
	panelDial       = "unix//config/" + PanelSocketName
)

// Router abstracts reverse proxy lifecycle and atomic multi-domain route syncing.
type Router interface {
	EnsureRunning(ctx context.Context) error
	Sync(ctx context.Context) error
	AutoDomain(serviceSlug, projectSlug string) string
	VerifyDNS(ctx context.Context, hostname string) (verified bool, resolvedIPs []string, serverIP string, messageKo string)
}

// CaddyRouter builds Caddy JSON configs and pushes updates to 127.0.0.1:2019/load.
type CaddyRouter struct {
	mu             sync.Mutex
	store          *db.Store
	engine         docker.Engine
	adminURL       string
	ingressNetwork string
	dataDir        string
	baseDomain     string
	httpClient     *http.Client

	ipMu     sync.Mutex
	cachedIP string
}

// NewCaddyRouter constructs a Caddy Admin API client backed by SQLite routing state.
func NewCaddyRouter(store *db.Store, engine docker.Engine, adminURL, ingressNetwork, dataDir, baseDomain string) *CaddyRouter {
	if adminURL == "" {
		adminURL = "http://127.0.0.1:2019"
	}
	return &CaddyRouter{
		store:          store,
		engine:         engine,
		adminURL:       strings.TrimRight(adminURL, "/"),
		ingressNetwork: ingressNetwork,
		dataDir:        dataDir,
		baseDomain:     strings.TrimSpace(baseDomain),
		httpClient:     &http.Client{Timeout: 8 * time.Second},
	}
}

// EnsureRunning verifies or boots the ssc-caddy container bound strictly to 80/443 and 127.0.0.1:2019.
func (c *CaddyRouter) EnsureRunning(ctx context.Context) error {
	if c.engine == nil {
		return nil
	}
	if err := c.engine.EnsureNetwork(ctx, c.ingressNetwork); err != nil {
		return err
	}

	initConfigHostPath := filepath.Join(c.dataDir, "caddy", "config", "init.json")
	if _, err := os.Stat(initConfigHostPath); os.IsNotExist(err) {
		initPayload := []byte(`{"admin":{"listen":"0.0.0.0:2019"}}`)
		if err := os.MkdirAll(filepath.Dir(initConfigHostPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(initConfigHostPath, initPayload, 0o644); err != nil {
			return err
		}
	}

	info, err := c.engine.InspectContainer(ctx, CaddyContainerName)
	if err == nil && info.Running {
		return c.Sync(ctx)
	}
	if err == nil {
		_ = c.engine.RemoveContainer(ctx, CaddyContainerName)
	}

	caddyDataDir := filepath.Join(c.dataDir, "caddy", "data")
	caddyConfigDir := filepath.Join(c.dataDir, "caddy", "config")
	if err := os.MkdirAll(caddyDataDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(caddyConfigDir, 0o755); err != nil {
		return err
	}

	_, err = c.engine.StartContainer(ctx, docker.ContainerSpec{
		Name:           CaddyContainerName,
		Image:          CaddyImage,
		Cmd:            []string{"caddy", "run", "--config", "/config/init.json"},
		AttachIngress:  true,
		IngressNetwork: c.ingressNetwork,
		RestartPolicy:  "unless-stopped",
		ExtraHosts:     []string{"host.docker.internal:host-gateway"},
		Labels: map[string]string{
			docker.LabelKind: "proxy",
		},
		Mounts: []docker.MountSpec{
			{Type: "bind", Source: caddyDataDir, Target: "/data"},
			{Type: "bind", Source: caddyConfigDir, Target: "/config"},
		},
		Ports: []docker.PortBinding{
			{HostIP: "0.0.0.0", HostPort: "80", ContainerPort: "80/tcp"},
			{HostIP: "0.0.0.0", HostPort: "443", ContainerPort: "443/tcp"},
			{HostIP: "127.0.0.1", HostPort: "2019", ContainerPort: "2019/tcp"},
		},
	})
	if err != nil {
		return fmt.Errorf("start caddy container: %w", err)
	}

	// Wait briefly until Caddy admin API accepts requests.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.Sync(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	return c.Sync(ctx)
}

// Sync compiles all active domains from SQLite into Caddy JSON and reloads atomically.
func (c *CaddyRouter) Sync(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	targets, err := c.store.ListAllRouteTargets()
	if err != nil {
		return fmt.Errorf("list route targets: %w", err)
	}
	panelDomain, _, _ := c.store.GetSetting("panel_domain")
	if panelDomain == "" && c.baseDomain != "" {
		panelDomain = "ssc." + c.baseDomain
	}

	cfg := BuildCaddyConfig(targets, panelDomain)
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal caddy config: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL+"/load", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post caddy /load: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		rawErr, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("caddy /load failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(rawErr)))
	}
	return nil
}

// BuildCaddyConfig builds a clean Caddy v2 JSON structure supporting multi-domain HTTPS & HTTP fallback.
func BuildCaddyConfig(targets []db.RouteTarget, panelDomain string) map[string]any {
	routes := make([]map[string]any, 0, len(targets)+1)
	var skipAutoHTTPS []string

	if panelDomain = strings.TrimSpace(panelDomain); panelDomain != "" {
		routes = append(routes, map[string]any{
			"match": []map[string]any{{"host": []string{panelDomain}}},
			"handle": []map[string]any{{
				"handler":   "reverse_proxy",
				"upstreams": []map[string]any{{"dial": panelDial}},
			}},
			"terminal": true,
		})
	}

	for _, t := range targets {
		host := strings.ToLower(strings.TrimSpace(t.Domain.Hostname))
		if host == "" {
			continue
		}

		// Prevent Let's Encrypt rate-limit errors on domains whose DNS has not pointed yet.
		useHTTPS := t.Domain.HTTPSEnabled && (t.Domain.DNSVerified || t.Domain.IsAuto)
		if !useHTTPS {
			skipAutoHTTPS = append(skipAutoHTTPS, host)
		}

		var handler map[string]any
		if t.Active && t.ActiveContainerName != "" && t.Port > 0 {
			handler = map[string]any{
				"handler":   "reverse_proxy",
				"upstreams": []map[string]any{{"dial": fmt.Sprintf("%s:%d", t.ActiveContainerName, t.Port)}},
			}
		} else {
			handler = map[string]any{
				"handler":     "static_response",
				"status_code": 503,
				"headers": map[string][]string{
					"Content-Type": {"text/plain; charset=utf-8"},
				},
				"body": "서비스가 아직 배포되지 않았거나 중지 상태입니다 (simple-server-control).",
			}
		}

		routes = append(routes, map[string]any{
			"match":    []map[string]any{{"host": []string{host}}},
			"handle":   []map[string]any{handler},
			"terminal": true,
		})
	}

	autoHTTPS := map[string]any{}
	if len(skipAutoHTTPS) > 0 {
		autoHTTPS["skip"] = skipAutoHTTPS
	}

	serverCfg := map[string]any{
		"listen":          []string{":80", ":443"},
		"routes":          routes,
		"automatic_https": autoHTTPS,
	}

	return map[string]any{
		"admin": map[string]any{
			// Bound to host 127.0.0.1:2019 via Docker port publish so external internet cannot hit it.
			"listen": "0.0.0.0:2019",
		},
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"main": serverCfg,
				},
			},
		},
	}
}

// AutoDomain computes an automatic domain using configured base domain or sslip.io wildcard DNS.
func (c *CaddyRouter) AutoDomain(serviceSlug, projectSlug string) string {
	sub := serviceSlug
	if projectSlug != "" {
		sub = serviceSlug + "-" + projectSlug
	}
	if c.baseDomain != "" {
		return sub + "." + c.baseDomain
	}
	ip := c.PublicIP(context.Background())
	if ip == "" {
		ip = "127.0.0.1"
	}
	dashed := strings.ReplaceAll(ip, ".", "-")
	return fmt.Sprintf("%s.%s.sslip.io", sub, dashed)
}

// VerifyDNS resolves A/AAAA records for hostname and compares them against the VPS public IP.
func (c *CaddyRouter) VerifyDNS(ctx context.Context, hostname string) (bool, []string, string, string) {
	serverIP := c.PublicIP(ctx)
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupHost(lookupCtx, hostname)
	if err != nil {
		return false, nil, serverIP, fmt.Sprintf("도메인 DNS 조회에 실패했습니다: %v (도메인 관리 화면에서 A 레코드가 서버 IP %s 를 가리키는지 확인해 주세요)", err, serverIP)
	}
	if len(ips) > 0 && allCloudflare(ips) {
		// Orange cloud hides the origin IP, so the A-record comparison below can never match.
		return true, ips, serverIP, fmt.Sprintf("Cloudflare 프록시(주황 구름)를 거쳐 연결된 도메인입니다. Cloudflare DNS의 A 레코드가 이 서버 IP(%s)인지 직접 확인해 주세요. %s", serverIP, CloudflareGuideKo)
	}
	if serverIP == "" {
		return len(ips) > 0, ips, serverIP, "도메인은 조회되었으나 서버 공인 IP를 자동 감지할 수 없습니다."
	}
	for _, resolved := range ips {
		if resolved == serverIP {
			return true, ips, serverIP, fmt.Sprintf("DNS 연결이 확인되었습니다 (%s → %s). HTTPS 인증서가 자동으로 발급됩니다.", hostname, serverIP)
		}
	}
	return false, ips, serverIP, fmt.Sprintf("도메인이 다른 IP(%s)를 가리키고 있습니다. DNS A 레코드를 이 서버 IP(%s)로 변경해 주세요.", strings.Join(ips, ", "), serverIP)
}

func allCloudflare(ips []string) bool {
	for _, ip := range ips {
		if !IsCloudflareIP(ip) {
			return false
		}
	}
	return true
}

// PublicIP detects the server's public IPv4 address (cached once resolved) or falls back to local non-loopback IP.
func (c *CaddyRouter) PublicIP(ctx context.Context) string {
	c.ipMu.Lock()
	if c.cachedIP != "" {
		ip := c.cachedIP
		c.ipMu.Unlock()
		return ip
	}
	c.ipMu.Unlock()

	if envIP := strings.TrimSpace(os.Getenv("SSC_PUBLIC_IP")); envIP != "" {
		c.ipMu.Lock()
		c.cachedIP = envIP
		c.ipMu.Unlock()
		return envIP
	}

	endpoints := []string{"https://api4.ipify.org", "https://ifconfig.me/ip"}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, ep := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		_ = resp.Body.Close()
		ip := strings.TrimSpace(string(raw))
		if parsed := net.ParseIP(ip); parsed != nil {
			c.ipMu.Lock()
			c.cachedIP = ip
			c.ipMu.Unlock()
			return ip
		}
	}

	// Offline/local fallback
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return ""
}
