// Package updater implements checksum-verified self-upgrades and systemd timer management for ssc.
package updater

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultRepo is the upstream GitHub owner/repository hosting release binaries.
	DefaultRepo = "gachon-star-want/simple-server-control"
	// MainServiceName is the systemd service running the ssc web/API server.
	MainServiceName = "simple-server-control"
	// UpdateServiceName is the oneshot systemd service triggered by UpdateTimerName.
	UpdateServiceName = "simple-server-control-update.service"
	// UpdateTimerName is the daily systemd timer for automated ssc upgrades.
	UpdateTimerName = "simple-server-control-update.timer"
)

// SemVer holds parsed major/minor/patch components and optional pre-release tag.
type SemVer struct {
	Major int
	Minor int
	Patch int
	Pre   string
}

// UpdateStatus describes the current version, latest release tag, and timer state.
type UpdateStatus struct {
	CurrentVersion    string `json:"current_version"`
	LatestVersion     string `json:"latest_version"`
	UpdateAvailable   bool   `json:"update_available"`
	IsDevBuild        bool   `json:"is_dev_build"`
	AutoUpdateEnabled bool   `json:"auto_update_enabled"`
	AutoUpdateNextRun string `json:"auto_update_next_run,omitempty"`
	SystemdManaged    bool   `json:"systemd_managed"`
	SystemdAvailable  bool   `json:"systemd_available"`
	Repo              string `json:"repo"`
	CheckError        string `json:"check_error,omitempty"`
}

// UpgradeResult summarizes the outcome of ApplyUpgrade.
type UpgradeResult struct {
	PreviousVersion string `json:"previous_version"`
	NewVersion      string `json:"new_version"`
	Updated         bool   `json:"updated"`
	SkippedReason   string `json:"skipped_reason,omitempty"`
	RestartRequired bool   `json:"restart_required"`
}

// Options configures a self-upgrade check or binary swap.
type Options struct {
	Repo             string
	CurrentVersion   string
	TargetPath       string
	OS               string
	Arch             string
	Force            bool
	AutoMode         bool
	GitHubWebBaseURL string
	HTTPClient       *http.Client
	VerifyBinary     func(ctx context.Context, path string) error
	ActiveDeployFn   func(ctx context.Context) (int64, error)
}

// Client handles release discovery, verification, atomic replacement, and timer setup.
type Client struct {
	opts Options
}

// New constructs an updater Client with defaults suitable for production Ubuntu hosts.
func New(opts Options) *Client {
	if strings.TrimSpace(opts.Repo) == "" {
		opts.Repo = DefaultRepo
	}
	if strings.TrimSpace(opts.GitHubWebBaseURL) == "" {
		opts.GitHubWebBaseURL = "https://github.com"
	}
	opts.GitHubWebBaseURL = strings.TrimRight(opts.GitHubWebBaseURL, "/")
	if strings.TrimSpace(opts.OS) == "" {
		opts.OS = runtime.GOOS
	}
	if strings.TrimSpace(opts.Arch) == "" {
		opts.Arch = runtime.GOARCH
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 90 * time.Second}
	}
	if opts.VerifyBinary == nil {
		opts.VerifyBinary = defaultVerifyBinary
	}
	if strings.TrimSpace(opts.TargetPath) == "" {
		if exe, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(exe); err == nil {
				opts.TargetPath = resolved
			} else {
				opts.TargetPath = exe
			}
		}
	}
	return &Client{opts: opts}
}

// ParseSemVer parses a version string like "v0.2.1" or "0.1.0-dev".
func ParseSemVer(raw string) (SemVer, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(raw, "v"))
	if s == "" {
		return SemVer{}, false
	}
	var pre string
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return SemVer{}, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil || maj < 0 || min < 0 || pat < 0 {
		return SemVer{}, false
	}
	return SemVer{Major: maj, Minor: min, Patch: pat, Pre: pre}, true
}

// IsNewerVersion returns true when latest strictly supersedes current.
// Local "-dev" builds return false unless force is true.
func IsNewerVersion(current, latest string, force bool) bool {
	if latest == "" {
		return false
	}
	if strings.HasSuffix(strings.TrimSpace(current), "-dev") && !force {
		return false
	}
	if force && strings.TrimPrefix(current, "v") != strings.TrimPrefix(latest, "v") {
		return true
	}
	cur, ok1 := ParseSemVer(current)
	lat, ok2 := ParseSemVer(latest)
	if !ok1 || !ok2 {
		return false
	}
	if lat.Major != cur.Major {
		return lat.Major > cur.Major
	}
	if lat.Minor != cur.Minor {
		return lat.Minor > cur.Minor
	}
	if lat.Patch != cur.Patch {
		return lat.Patch > cur.Patch
	}
	// Stable release supersedes pre-release of the same triple.
	if cur.Pre != "" && lat.Pre == "" {
		return true
	}
	return false
}

// LatestReleaseTag resolves the newest published release tag via GitHub's /releases/latest redirect
// without consuming unauthenticated GitHub REST API rate limits.
func (c *Client) LatestReleaseTag(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/%s/releases/latest", c.opts.GitHubWebBaseURL, c.opts.Repo)
	noRedirectClient := *c.opts.HTTPClient
	noRedirectClient.Timeout = 12 * time.Second
	noRedirectClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "simple-server-control-updater")

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("최신 릴리스 조회 실패: %w", err)
	}
	_ = resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" && resp.StatusCode == http.StatusMethodNotAllowed {
		getReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		getReq.Header.Set("User-Agent", "simple-server-control-updater")
		resp, err = noRedirectClient.Do(getReq)
		if err != nil {
			return "", fmt.Errorf("최신 릴리스 조회 실패: %w", err)
		}
		_ = resp.Body.Close()
		loc = resp.Header.Get("Location")
	}

	if idx := strings.LastIndex(loc, "/releases/tag/"); idx >= 0 {
		tag := strings.TrimSpace(loc[idx+len("/releases/tag/"):])
		tag = strings.SplitN(tag, "?", 2)[0]
		if tag != "" {
			return tag, nil
		}
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("아직 게시된 GitHub Release가 없습니다 (%s)", c.opts.Repo)
	}
	return "", fmt.Errorf("최신 릴리스 태그를 파싱할 수 없습니다 (status=%d)", resp.StatusCode)
}

// Status checks the latest release availability and local systemd auto-update timer state.
func (c *Client) Status(ctx context.Context) UpdateStatus {
	st := UpdateStatus{
		CurrentVersion:   c.opts.CurrentVersion,
		IsDevBuild:       strings.HasSuffix(strings.TrimSpace(c.opts.CurrentVersion), "-dev"),
		SystemdManaged:   RunningUnderSystemd(),
		SystemdAvailable: SystemctlAvailable(),
		Repo:             c.opts.Repo,
	}
	if st.SystemdAvailable {
		st.AutoUpdateEnabled, st.AutoUpdateNextRun = AutoUpdateTimerState(ctx)
	}
	latest, err := c.LatestReleaseTag(ctx)
	if err != nil {
		st.CheckError = err.Error()
		return st
	}
	st.LatestVersion = latest
	st.UpdateAvailable = IsNewerVersion(c.opts.CurrentVersion, latest, false)
	return st
}

// ApplyUpgrade checks for a newer verified release and atomically swaps TargetPath when ready.
func (c *Client) ApplyUpgrade(ctx context.Context) (*UpgradeResult, error) {
	if c.opts.OS != "linux" {
		return nil, fmt.Errorf("자동 바이너리 업그레이드는 Linux(Ubuntu VPS) 환경만 지원합니다 (현재: %s/%s)", c.opts.OS, c.opts.Arch)
	}
	if c.opts.Arch != "amd64" && c.opts.Arch != "arm64" {
		return nil, fmt.Errorf("지원하지 않는 CPU 아키텍처입니다: %s", c.opts.Arch)
	}

	latest, err := c.LatestReleaseTag(ctx)
	if err != nil {
		return nil, err
	}
	if !IsNewerVersion(c.opts.CurrentVersion, latest, c.opts.Force) {
		reason := "이미 최신 버전입니다."
		if strings.HasSuffix(strings.TrimSpace(c.opts.CurrentVersion), "-dev") && !c.opts.Force {
			reason = "로컬 개발 빌드(-dev)에서는 자동 덮어쓰기를 건너뜁니다 (--force 로 강제 적용 가능)."
		}
		return &UpgradeResult{
			PreviousVersion: c.opts.CurrentVersion,
			NewVersion:      latest,
			Updated:         false,
			SkippedReason:   reason,
		}, nil
	}

	if c.opts.ActiveDeployFn != nil && (!c.opts.Force || c.opts.AutoMode) {
		if active, err := c.opts.ActiveDeployFn(ctx); err == nil && active > 0 {
			return &UpgradeResult{
				PreviousVersion: c.opts.CurrentVersion,
				NewVersion:      latest,
				Updated:         false,
				SkippedReason:   fmt.Sprintf("현재 %d건의 서비스 배포가 진행 중이어서 안전을 위해 업데이트를 다음 주기로 연기합니다.", active),
			}, nil
		}
	}

	assetName := fmt.Sprintf("simple-server-control-%s-%s", c.opts.OS, c.opts.Arch)
	baseDL := fmt.Sprintf("%s/%s/releases/download/%s", c.opts.GitHubWebBaseURL, c.opts.Repo, latest)

	checksumRaw, err := c.downloadBytes(ctx, baseDL+"/checksums.txt", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("checksums.txt 다운로드 실패: %w", err)
	}
	expectedSum, ok := parseChecksumForFile(string(checksumRaw), assetName)
	if !ok {
		return nil, fmt.Errorf("checksums.txt에서 %s 체크섬을 찾을 수 없습니다", assetName)
	}

	targetDir := filepath.Dir(c.opts.TargetPath)
	if targetDir == "" || targetDir == "." {
		targetDir = "/usr/local/bin"
	}
	// Stage temp binary inside targetDir so os.Rename stays atomic on the same filesystem.
	tmpFile, err := os.CreateTemp(targetDir, ".ssc-upgrade-*")
	if err != nil {
		return nil, fmt.Errorf("업그레이드 임시 파일 생성 실패 (sudo 권한 필요): %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		_ = tmpFile.Close()
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	actualSum, err := c.downloadToFile(ctx, baseDL+"/"+assetName, tmpFile)
	if err != nil {
		return nil, fmt.Errorf("바이너리 다운로드 실패: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(actualSum, expectedSum) {
		return nil, fmt.Errorf("SHA-256 체크섬 불일치 (expected=%s actual=%s)", expectedSum, actualSum)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return nil, fmt.Errorf("실행 권한 부여 실패: %w", err)
	}
	if err := c.opts.VerifyBinary(ctx, tmpPath); err != nil {
		return nil, fmt.Errorf("새 바이너리 실행 검증 실패: %w", err)
	}
	if err := os.Rename(tmpPath, c.opts.TargetPath); err != nil {
		return nil, fmt.Errorf("바이너리 교체 실패: %w", err)
	}
	cleanupTmp = false

	return &UpgradeResult{
		PreviousVersion: c.opts.CurrentVersion,
		NewVersion:      latest,
		Updated:         true,
		RestartRequired: true,
	}, nil
}

func (c *Client) downloadBytes(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "simple-server-control-updater")
	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}

func (c *Client) downloadToFile(ctx context.Context, url string, dst *os.File) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "simple-server-control-updater")
	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, hasher), io.LimitReader(resp.Body, 256<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func parseChecksumForFile(content, filename string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		sum := strings.TrimSpace(fields[0])
		name := strings.TrimPrefix(strings.TrimSpace(fields[1]), "*")
		if filepath.Base(name) == filename && len(sum) == 64 {
			return strings.ToLower(sum), true
		}
	}
	return "", false
}

func defaultVerifyBinary(ctx context.Context, path string) error {
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunningUnderSystemd reports whether the current process is supervised by systemd.
func RunningUnderSystemd() bool {
	return runtime.GOOS == "linux" && strings.TrimSpace(os.Getenv("INVOCATION_ID")) != ""
}

// SystemctlAvailable returns true on systemd-based Linux systems.
func SystemctlAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}

// AutoUpdateTimerState reports whether simple-server-control-update.timer is enabled and active.
func AutoUpdateTimerState(ctx context.Context) (enabled bool, nextRun string) {
	if !SystemctlAvailable() {
		return false, ""
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "systemctl", "is-enabled", UpdateTimerName).Output()
	if err != nil || strings.TrimSpace(string(out)) != "enabled" {
		return false, ""
	}
	nextOut, err := exec.CommandContext(cctx, "systemctl", "show", UpdateTimerName, "-p", "NextElapseUSecRealtime", "--value").Output()
	if err == nil {
		nextRun = strings.TrimSpace(string(nextOut))
		if nextRun == "n/a" {
			nextRun = ""
		}
	}
	return true, nextRun
}

// EnableAutoUpdate writes the systemd service & timer units and activates daily automatic updates.
func EnableAutoUpdate(ctx context.Context, binPath, dataDir, repo string) error {
	if !SystemctlAvailable() {
		return errors.New("systemd 환경(Linux)이 아니므로 자동 업데이트 타이머를 등록할 수 없습니다")
	}
	if strings.TrimSpace(binPath) == "" {
		binPath = "/usr/local/bin/ssc"
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "/var/lib/ssc"
	}
	if strings.TrimSpace(repo) == "" {
		repo = DefaultRepo
	}

	serviceUnit := fmt.Sprintf(`[Unit]
Description=Simple Server Control (ssc) Auto-Updater
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=%s upgrade --auto
Environment=SSC_DATA_DIR=%s
Environment=SSC_REPO=%s
`, binPath, dataDir, repo)

	timerUnit := `[Unit]
Description=Daily auto-update check for Simple Server Control (ssc)

[Timer]
OnCalendar=daily
RandomizedDelaySec=30m
Persistent=true

[Install]
WantedBy=timers.target
`

	svcPath := filepath.Join("/etc/systemd/system", UpdateServiceName)
	tmrPath := filepath.Join("/etc/systemd/system", UpdateTimerName)
	if err := os.WriteFile(svcPath, []byte(serviceUnit), 0o644); err != nil {
		return fmt.Errorf("systemd 서비스 파일 작성 실패 (sudo 권한 필요): %w", err)
	}
	if err := os.WriteFile(tmrPath, []byte(timerUnit), 0o644); err != nil {
		return fmt.Errorf("systemd 타이머 파일 작성 실패 (sudo 권한 필요): %w", err)
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload 실패: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "enable", "--now", UpdateTimerName).CombinedOutput(); err != nil {
		return fmt.Errorf("자동 업데이트 타이머 활성화 실패: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DisableAutoUpdate stops and disables the daily auto-update systemd timer.
func DisableAutoUpdate(ctx context.Context) error {
	if !SystemctlAvailable() {
		return errors.New("systemd 환경(Linux)이 아닙니다")
	}
	out, err := exec.CommandContext(ctx, "systemctl", "disable", "--now", UpdateTimerName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("자동 업데이트 타이머 비활성화 실패: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RestartMainService triggers a systemd restart of simple-server-control after a CLI upgrade.
func RestartMainService(ctx context.Context) error {
	if !SystemctlAvailable() {
		return nil
	}
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", MainServiceName).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "active" {
		return nil
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", MainServiceName).CombinedOutput(); err != nil {
		return fmt.Errorf("서비스 재시작 실패: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CheckHealthzActiveDeploys queries the local ssc /healthz endpoint for live in-memory deployment count.
func CheckHealthzActiveDeploys(ctx context.Context, listenAddr string) (int64, bool) {
	port := "8080"
	if _, p, err := net.SplitHostPort(listenAddr); err == nil && p != "" {
		port = p
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
	if err != nil {
		return 0, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return 0, false
	}
	defer resp.Body.Close()

	var payload struct {
		OK                bool  `json:"ok"`
		ActiveDeployments int64 `json:"active_deployments"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload); err != nil {
		return 0, false
	}
	return payload.ActiveDeployments, true
}
