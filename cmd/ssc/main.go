// Command ssc runs the simple-server-control PaaS control plane, diagnostics, and recovery tools.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/api"
	"github.com/gachon-star-want/simple-server-control/internal/build"
	"github.com/gachon-star-want/simple-server-control/internal/config"
	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/deploy"
	"github.com/gachon-star-want/simple-server-control/internal/docker"
	"github.com/gachon-star-want/simple-server-control/internal/doctor"
	"github.com/gachon-star-want/simple-server-control/internal/github"
	"github.com/gachon-star-want/simple-server-control/internal/proxy"
	"github.com/gachon-star-want/simple-server-control/web"
	"golang.org/x/crypto/bcrypt"
)

// Populated by GoReleaser / build -ldflags.
var (
	Version = "0.1.0-dev"
	Commit  = "local"
	Date    = "unknown"
)

func main() {
	sub := "serve"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		sub = os.Args[1]
	}

	var err error
	switch sub {
	case "serve":
		err = runServe()
	case "doctor":
		err = runDoctor()
	case "reset-password":
		err = runResetPassword(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("simple-server-control (ssc) %s (commit %s, built %s)\n", Version, Commit, Date)
	case "help", "-h", "--help":
		printUsage()
	default:
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "오류: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`Simple Server Control (ssc) - 경량 한국어 셀프호스팅 PaaS

사용법:
  ssc serve                    컨트롤 플레인 및 웹 대시보드 실행 (기본값)
  ssc doctor                   서버 환경(Ubuntu LTS, Docker, Caddy, Swap) 진단
  ssc reset-password           관리자 비밀번호 초기화
  ssc version                  버전 정보 출력
`)
}

func openBackend(cfg *config.Config) (*db.Store, docker.Engine, error) {
	vault, err := crypto.LoadOrCreateVault(cfg.MasterKeyPath)
	if err != nil {
		return nil, nil, err
	}
	store, err := db.Open(filepath.Join(cfg.DataDir, "ssc.db"), vault)
	if err != nil {
		return nil, nil, err
	}
	eng, err := docker.NewClient()
	if err != nil {
		slog.Warn("Docker 소켓 초기화 실패 — UI/설정 전용 모드로 계속합니다", "error", err)
		return store, nil, nil
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := eng.Ping(pingCtx); err != nil {
		slog.Warn("Docker 데몬 응답 없음 — Docker 기능은 데몬 기동 후 활성화됩니다", "error", err)
	}
	return store, eng, nil
}

func runServe() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store, eng, err := openBackend(cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	if eng != nil {
		defer eng.Close()
	}

	uiFS, err := web.DistFS()
	if err != nil {
		return fmt.Errorf("load embedded UI: %w", err)
	}

	hub := api.NewCanvasHub()
	onChange := func(projectID, envID, serviceID, status string) {
		hub.Broadcast(api.CanvasEvent{
			Type:          "service_status",
			ProjectID:     projectID,
			EnvironmentID: envID,
			ServiceID:     serviceID,
			Status:        status,
		})
	}

	rtr := proxy.NewCaddyRouter(store, eng, cfg.CaddyAdminURL, cfg.IngressNetwork, cfg.DataDir, cfg.ListenAddr, cfg.BaseDomain)
	builder := build.NewContainerBuilder(eng, filepath.Join(cfg.DataDir, "repos"), cfg.BuildkitAddr)
	ghClient := github.NewClient(store, "")
	pipe := deploy.NewPipeline(store, eng, rtr, builder, ghClient, filepath.Join(cfg.DataDir, "logs"), cfg.IngressNetwork, onChange)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if eng != nil {
		go func() {
			bootCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			if err := rtr.EnsureRunning(bootCtx); err != nil {
				slog.Warn("Caddy 프록시 자동 시작 경고 (포트 80/443 점유 여부 확인 필요)", "error", err)
			} else {
				slog.Info("Caddy 다중 도메인 프록시 준비 완료")
			}
		}()
		watcher := docker.NewWatcher(eng, store, onChange)
		go watcher.Run(ctx)
	}

	srv := api.NewServer(cfg, store, eng, rtr, pipe, ghClient, hub, uiFS)
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if count, _ := store.CountUsers(); count == 0 {
		fmt.Println("================================================================")
		fmt.Println(" Simple Server Control 초기 설정이 필요합니다")
		fmt.Printf("  대시보드 주소 : http://<서버IP>%s\n", cfg.ListenAddr)
		fmt.Printf("  Setup Token   : %s\n", cfg.SetupToken)
		fmt.Println("================================================================")
	}

	slog.Info("Simple Server Control 시작", "listen", cfg.ListenAddr, "data_dir", cfg.DataDir, "version", Version)

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutCtx)
	case serveErr := <-errCh:
		return serveErr
	}
}

func runDoctor() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	eng, err := docker.NewClient()
	if err == nil {
		defer eng.Close()
	}

	checks := doctor.RunAll(context.Background(), eng, cfg.DataDir, cfg.CaddyAdminURL)
	fmt.Println("=== Simple Server Control 서버 진단 (ssc doctor) ===")
	hasFail := false
	for _, c := range checks {
		badge := "[OK]"
		if c.Status == "warn" {
			badge = "[WARN]"
		} else if c.Status == "fail" {
			badge = "[FAIL]"
			hasFail = true
		}
		fmt.Printf("%-7s %s: %s\n", badge, c.Name, c.MessageKo)
		if c.RemediationKo != "" {
			fmt.Printf("        해결 방법: %s\n", c.RemediationKo)
		}
	}
	if hasFail {
		return fmt.Errorf("하나 이상의 필수 항목에서 문제가 발견되었습니다")
	}
	return nil
}

func runResetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	pwFlag := fs.String("password", "", "새 관리자 비밀번호 (8자 이상)")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	vault, err := crypto.LoadOrCreateVault(cfg.MasterKeyPath)
	if err != nil {
		return err
	}
	store, err := db.Open(filepath.Join(cfg.DataDir, "ssc.db"), vault)
	if err != nil {
		return err
	}
	defer store.Close()

	pw := strings.TrimSpace(*pwFlag)
	if pw == "" {
		fmt.Print("새 관리자 비밀번호 입력 (8자 이상): ")
		reader := bufio.NewReader(os.Stdin)
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return readErr
		}
		pw = strings.TrimSpace(line)
	}
	if len(pw) < 8 {
		return fmt.Errorf("비밀번호는 8자 이상이어야 합니다")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	email, err := store.UpdateFirstAdminPassword(string(hash))
	if err != nil {
		return err
	}
	fmt.Printf("관리자(%s) 비밀번호가 성공적으로 변경되었습니다.\n", email)
	return nil
}
