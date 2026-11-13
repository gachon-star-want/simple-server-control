// Package doctor inspects host OS compatibility, Docker daemon safety (Snap/address-pools),
// Caddy, Railpack, swap/memory, firewall, and free disk capacity with Korean guidance.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/docker"
)

// CheckResult reports one diagnostic item with non-developer friendly Korean text.
type CheckResult struct {
	Name          string `json:"name"`
	Status        string `json:"status"` // "ok" | "warn" | "fail"
	MessageKo     string `json:"message_ko"`
	RemediationKo string `json:"remediation_ko,omitempty"`
}

// RunAll executes all host and container runtime health checks.
func RunAll(ctx context.Context, engine docker.Engine, dataDir, caddyAdminURL string) []CheckResult {
	results := []CheckResult{
		checkOS(),
		checkSnapDocker(),
		checkDockerEngine(ctx, engine),
		checkDockerAddressPool(),
		checkCaddyAdmin(ctx, caddyAdminURL),
		checkTools(),
		checkDiskSpace(dataDir),
		checkMemoryAndSwap(),
	}
	return results
}

func checkOS() CheckResult {
	if runtime.GOOS != "linux" {
		return CheckResult{
			Name:          "운영체제 (OS)",
			Status:        "warn",
			MessageKo:     fmt.Sprintf("현재 %s/%s 환경입니다 (개발 모드). 실제 배포는 Ubuntu 22.04/24.04/26.04 LTS를 권장합니다.", runtime.GOOS, runtime.GOARCH),
			RemediationKo: "운영 서버(VPS)에서는 install.sh로 Ubuntu 22.04, 24.04 또는 26.04 LTS에 설치해 주세요.",
		}
	}
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return CheckResult{Name: "운영체제 (OS)", Status: "warn", MessageKo: "/etc/os-release를 읽을 수 없습니다."}
	}
	text := string(raw)
	if strings.Contains(text, "ID=ubuntu") || strings.Contains(text, "ID=\"ubuntu\"") {
		switch {
		case strings.Contains(text, "22.04"):
			return CheckResult{Name: "운영체제 (OS)", Status: "ok", MessageKo: "Ubuntu 22.04 LTS (Jammy) 지원 환경 확인됨"}
		case strings.Contains(text, "24.04"):
			return CheckResult{Name: "운영체제 (OS)", Status: "ok", MessageKo: "Ubuntu 24.04 LTS (Noble) 지원 환경 확인됨"}
		case strings.Contains(text, "26.04"):
			return CheckResult{Name: "운영체제 (OS)", Status: "ok", MessageKo: "Ubuntu 26.04 LTS (Resolute) 지원 환경 확인됨"}
		}
	}
	return CheckResult{
		Name:          "운영체제 (OS)",
		Status:        "warn",
		MessageKo:     "공식 테스트된 Ubuntu LTS(22.04/24.04/26.04) 외의 리눅스 배포판입니다.",
		RemediationKo: "Ubuntu 22.04, 24.04 또는 26.04 LTS 사용을 권장합니다.",
	}
}

func checkSnapDocker() CheckResult {
	if runtime.GOOS != "linux" {
		return CheckResult{Name: "Snap Docker 충돌 점검", Status: "ok", MessageKo: "Linux 환경이 아니므로 해당 없음"}
	}
	if _, err := exec.LookPath("snap"); err != nil {
		return CheckResult{Name: "Snap Docker 충돌 점검", Status: "ok", MessageKo: "Snap Docker 미설치 (정상)"}
	}
	out, err := exec.Command("snap", "list", "docker").CombinedOutput()
	if err == nil && strings.Contains(string(out), "docker") {
		return CheckResult{
			Name:          "Snap Docker 충돌 점검",
			Status:        "fail",
			MessageKo:     "Snap 버전 Docker가 설치되어 있습니다. 네트워크/파일 권한 충돌로 배포가 실패할 수 있습니다.",
			RemediationKo: "sudo snap remove --purge docker 실행 후 공식 Docker CE 저장소에서 설치해 주세요.",
		}
	}
	return CheckResult{Name: "Snap Docker 충돌 점검", Status: "ok", MessageKo: "Snap Docker 없음 (공식 Docker CE 환경)"}
}

func checkDockerEngine(ctx context.Context, engine docker.Engine) CheckResult {
	if engine == nil {
		return CheckResult{
			Name:          "Docker 엔진 연결",
			Status:        "fail",
			MessageKo:     "Docker 클라이언트가 초기화되지 않았습니다.",
			RemediationKo: "Docker 데몬 실행 상태(systemctl status docker)와 소켓 권한을 확인해 주세요.",
		}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := engine.Ping(pingCtx); err != nil {
		return CheckResult{
			Name:          "Docker 엔진 연결",
			Status:        "fail",
			MessageKo:     fmt.Sprintf("Docker 데몬과 통신할 수 없습니다: %v", err),
			RemediationKo: "sudo systemctl enable --now docker 를 실행한 뒤 다시 확인해 주세요.",
		}
	}
	return CheckResult{Name: "Docker 엔진 연결", Status: "ok", MessageKo: "Docker 엔진 정상 응답 중"}
}

func checkDockerAddressPool() CheckResult {
	if runtime.GOOS != "linux" {
		return CheckResult{Name: "Docker 네트워크 IP 대역 풀", Status: "ok", MessageKo: "로컬 개발 환경"}
	}
	raw, err := os.ReadFile("/etc/docker/daemon.json")
	if err != nil {
		return CheckResult{
			Name:          "Docker 네트워크 IP 대역 풀",
			Status:        "warn",
			MessageKo:     "/etc/docker/daemon.json 설정 파일이 없습니다. 프로젝트 수가 30개를 넘으면 Docker 네트워크 IP가 고갈될 수 있습니다.",
			RemediationKo: "install.sh를 다시 실행하거나 default-address-pools 설정을 추가해 주세요.",
		}
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		return CheckResult{Name: "Docker 네트워크 IP 대역 풀", Status: "warn", MessageKo: "daemon.json 파싱 실패"}
	}
	if _, ok := parsed["default-address-pools"]; !ok {
		return CheckResult{
			Name:          "Docker 네트워크 IP 대역 풀",
			Status:        "warn",
			MessageKo:     "default-address-pools 미설정 — 다중 프로젝트 격리 네트워크 확장을 위해 설정을 권장합니다.",
			RemediationKo: "default-address-pools: [{\"base\":\"10.0.0.0/8\",\"size\":24}] 추가 후 Docker를 재시작하세요.",
		}
	}
	return CheckResult{Name: "Docker 네트워크 IP 대역 풀", Status: "ok", MessageKo: "다중 프로젝트 격리용 IP 대역 풀 설정 완료"}
}

func checkCaddyAdmin(ctx context.Context, adminURL string) CheckResult {
	if adminURL == "" {
		adminURL = "http://127.0.0.1:2019"
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(adminURL, "/")+"/config/", nil)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return CheckResult{
			Name:          "도메인 프록시 (Caddy)",
			Status:        "warn",
			MessageKo:     "ssc-caddy 관리 API가 아직 응답하지 않습니다 (서버 시작 시 자동 기동됩니다).",
			RemediationKo: "80/443 포트를 점유한 nginx/apache가 있다면 중지한 뒤 서비스를 재시작해 주세요.",
		}
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return CheckResult{Name: "도메인 프록시 (Caddy)", Status: "warn", MessageKo: fmt.Sprintf("Caddy Admin 응답 코드: %d", resp.StatusCode)}
	}
	return CheckResult{Name: "도메인 프록시 (Caddy)", Status: "ok", MessageKo: "Caddy 리버스 프록시 및 자동 HTTPS 준비 완료"}
}

func checkTools() CheckResult {
	_, gitErr := exec.LookPath("git")
	_, rpErr := exec.LookPath("railpack")
	switch {
	case gitErr != nil && rpErr != nil:
		return CheckResult{
			Name:          "빌드 도구 (Git / Railpack)",
			Status:        "fail",
			MessageKo:     "git 과 railpack CLI 모두 찾을 수 없습니다.",
			RemediationKo: "Ubuntu 설치 스크립트로 git과 railpack을 설치해 주세요.",
		}
	case rpErr != nil:
		return CheckResult{
			Name:          "빌드 도구 (Git / Railpack)",
			Status:        "warn",
			MessageKo:     "git은 설치됨, railpack CLI 미설치 (Dockerfile이 있는 프로젝트만 빌드 가능).",
			RemediationKo: "자동 런타임 감지를 쓰려면 install.sh로 railpack을 설치해 주세요.",
		}
	case gitErr != nil:
		return CheckResult{
			Name:          "빌드 도구 (Git / Railpack)",
			Status:        "fail",
			MessageKo:     "git 바이너리가 없습니다.",
			RemediationKo: "sudo apt-get install -y git",
		}
	default:
		return CheckResult{Name: "빌드 도구 (Git / Railpack)", Status: "ok", MessageKo: "git 및 railpack 빌드 도구 준비 완료"}
	}
}

func checkDiskSpace(dataDir string) CheckResult {
	if dataDir == "" {
		dataDir = "/"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dataDir, &st); err != nil {
		return CheckResult{Name: "디스크 여유 공간", Status: "warn", MessageKo: "디스크 용량을 확인할 수 없습니다."}
	}
	freeGB := float64(st.Bavail*uint64(st.Bsize)) / (1024 * 1024 * 1024)
	if freeGB < 2.0 {
		return CheckResult{
			Name:          "디스크 여유 공간",
			Status:        "fail",
			MessageKo:     fmt.Sprintf("여유 디스크가 %.1fGB로 매우 부족합니다.", freeGB),
			RemediationKo: "불필요한 Docker 이미지 정리(docker system prune -af) 또는 디스크 증설이 필요합니다.",
		}
	}
	return CheckResult{Name: "디스크 여유 공간", Status: "ok", MessageKo: fmt.Sprintf("%.1fGB 여유 공간 확보됨", freeGB)}
}

func checkMemoryAndSwap() CheckResult {
	if runtime.GOOS != "linux" {
		return CheckResult{Name: "메모리 및 Swap", Status: "ok", MessageKo: "로컬 개발 환경"}
	}
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return CheckResult{Name: "메모리 및 Swap", Status: "warn", MessageKo: "/proc/meminfo 조회 실패"}
	}
	var memKB, swapKB uint64
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			_, _ = fmt.Sscanf(line, "MemTotal: %d kB", &memKB)
		}
		if strings.HasPrefix(line, "SwapTotal:") {
			_, _ = fmt.Sscanf(line, "SwapTotal: %d kB", &swapKB)
		}
	}
	memGB := float64(memKB) / (1024 * 1024)
	swapGB := float64(swapKB) / (1024 * 1024)
	if memGB <= 2.5 && swapGB < 1.0 {
		return CheckResult{
			Name:          "메모리 및 Swap",
			Status:        "warn",
			MessageKo:     fmt.Sprintf("RAM %.1fGB + Swap %.1fGB — 빌드 시 OOM(메모리 부족)이 발생할 수 있습니다.", memGB, swapGB),
			RemediationKo: "install.sh 실행 또는 2GB 스왑 파일(/swapfile) 생성을 권장합니다.",
		}
	}
	return CheckResult{Name: "메모리 및 Swap", Status: "ok", MessageKo: fmt.Sprintf("RAM %.1fGB / Swap %.1fGB 정상", memGB, swapGB)}
}
