package deploy

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/build"
	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/docker"
	"github.com/gachon-star-want/simple-server-control/internal/github"
	"github.com/gachon-star-want/simple-server-control/internal/proxy"
)

// Pipeline stages emitted over database + WebSocket events.
const (
	StageCloning       = "cloning"
	StageBuilding      = "building"
	StageStartingNew   = "starting_new"
	StageHealthchecking = "healthchecking"
	StageSwitching     = "switching"
	StageDrainingOld   = "draining_old"
	StageSucceeded     = "succeeded"
	StageFailed        = "failed"
)

// LogEvent streams deployment progress chunks and stage transitions to WebSocket subscribers.
type LogEvent struct {
	DeploymentID string `json:"deployment_id"`
	ServiceID    string `json:"service_id"`
	ProjectID    string `json:"project_id"`
	Stage        string `json:"stage"`
	Line         string `json:"line,omitempty"`
	Done         bool   `json:"done,omitempty"`
	Status       string `json:"status,omitempty"`
}

// HealthProbe checks whether a newly launched candidate container serves HTTP/TCP traffic cleanly.
type HealthProbe func(ctx context.Context, addr, path string) error

// Pipeline coordinates database provisioning and zero-downtime app rollouts.
type Pipeline struct {
	store          *db.Store
	engine         docker.Engine
	router         proxy.Router
	builder        build.Runner
	ghClient       *github.Client
	logsDir        string
	ingressNetwork string
	drainDelay     time.Duration
	probeInterval  time.Duration
	probeFunc      HealthProbe
	onCanvasChange func(projectID, envID, serviceID, status string)

	svcLocksMu sync.Mutex
	svcLocks   map[string]*sync.Mutex

	subMu       sync.Mutex
	subscribers map[string][]chan LogEvent
}

// NewPipeline wires together storage, Docker, Caddy, GitHub, and build runner dependencies.
func NewPipeline(
	store *db.Store,
	engine docker.Engine,
	router proxy.Router,
	builder build.Runner,
	ghClient *github.Client,
	logsDir string,
	ingressNetwork string,
	onCanvasChange func(projectID, envID, serviceID, status string),
) *Pipeline {
	return &Pipeline{
		store:          store,
		engine:         engine,
		router:         router,
		builder:        builder,
		ghClient:       ghClient,
		logsDir:        logsDir,
		ingressNetwork: ingressNetwork,
		drainDelay:     2 * time.Second,
		probeInterval:  500 * time.Millisecond,
		probeFunc:      defaultHTTPProbe,
		onCanvasChange: onCanvasChange,
		svcLocks:       make(map[string]*sync.Mutex),
		subscribers:    make(map[string][]chan LogEvent),
	}
}

// SetTimings overrides drain/probe timing intervals (primarily used in deterministic tests).
func (p *Pipeline) SetTimings(drainDelay, probeInterval time.Duration) {
	if drainDelay >= 0 {
		p.drainDelay = drainDelay
	}
	if probeInterval > 0 {
		p.probeInterval = probeInterval
	}
}

// SetProbeFunc replaces the default HTTP/TCP candidate healthcheck probe (useful for mock tests).
func (p *Pipeline) SetProbeFunc(fn HealthProbe) {
	if fn != nil {
		p.probeFunc = fn
	}
}

// SubscribeLogs streams historical log lines (if any) followed by live deployment updates.
func (p *Pipeline) SubscribeLogs(ctx context.Context, deploymentID string) (<-chan LogEvent, error) {
	dep, err := p.store.GetDeployment(deploymentID)
	if err != nil {
		return nil, err
	}

	ch := make(chan LogEvent, 128)
	p.subMu.Lock()
	p.subscribers[deploymentID] = append(p.subscribers[deploymentID], ch)
	p.subMu.Unlock()

	go func() {
		defer func() {
			p.subMu.Lock()
			list := p.subscribers[deploymentID]
			for i, candidate := range list {
				if candidate == ch {
					p.subscribers[deploymentID] = append(list[:i], list[i+1:]...)
					break
				}
			}
			p.subMu.Unlock()
		}()

		if dep.LogPath != "" {
			if f, openErr := os.Open(dep.LogPath); openErr == nil {
				scanner := bufio.NewScanner(f)
				for scanner.Scan() {
					select {
					case ch <- LogEvent{
						DeploymentID: dep.ID,
						ServiceID:    dep.ServiceID,
						ProjectID:    dep.ProjectID,
						Stage:        dep.Stage,
						Line:         scanner.Text(),
					}:
					case <-ctx.Done():
						_ = f.Close()
						return
					}
				}
				_ = f.Close()
			}
		}

		if dep.Status == "succeeded" || dep.Status == "failed" {
			select {
			case ch <- LogEvent{
				DeploymentID: dep.ID,
				ServiceID:    dep.ServiceID,
				ProjectID:    dep.ProjectID,
				Stage:        dep.Stage,
				Status:       dep.Status,
				Done:         true,
			}:
			case <-ctx.Done():
			}
			return
		}

		<-ctx.Done()
	}()

	return ch, nil
}

// ProvisionDatabase creates isolated persistent storage, random credentials, and launches a DB container
// strictly on the project's isolated bridge network (never exposed on host ports or ssc-ingress).
func (p *Pipeline) ProvisionDatabase(ctx context.Context, svc *db.Service) error {
	proj, err := p.store.GetProject(svc.ProjectID)
	if err != nil {
		return err
	}

	spec, secrets, err := buildDatabaseContainerSpec(proj, svc)
	if err != nil {
		return err
	}

	for _, sec := range secrets {
		if err := p.store.UpsertSecret(svc.EnvironmentID, svc.ID, sec); err != nil {
			return fmt.Errorf("store database secret %s: %w", sec.Key, err)
		}
	}

	// Ensure env uses generated values cleanly.
	envMap, err := p.store.ResolveServiceRuntimeEnv(svc.ID)
	if err != nil {
		return err
	}
	spec.Env = formatDockerEnv(envMap)

	if err := p.engine.EnsureVolume(ctx, spec.Mounts[0].Source); err != nil {
		return err
	}
	info, err := p.engine.StartContainer(ctx, spec)
	if err != nil {
		_, _ = p.store.UpdateServiceRuntimeStatus(svc.ID, "failed", "", "", "", time.Now().UnixNano(), false)
		return err
	}

	_, err = p.store.UpdateServiceRuntimeStatus(svc.ID, "running", info.ID, info.Name, spec.Image, time.Now().UnixNano(), true)
	if p.onCanvasChange != nil {
		p.onCanvasChange(svc.ProjectID, svc.EnvironmentID, svc.ID, "running")
	}
	return err
}

// StartAppDeployment creates a pending deployment record immediately and executes the 6-stage
// zero-downtime rollout asynchronously, returning the deployment ID right away for live UI tracking.
func (p *Pipeline) StartAppDeployment(serviceID, triggerType, commitSHA, commitMsg string) (*db.Deployment, error) {
	svc, err := p.store.GetService(serviceID)
	if err != nil {
		return nil, err
	}
	if svc.Kind != "app" {
		return nil, fmt.Errorf("only app services support source deployments")
	}

	if err := os.MkdirAll(p.logsDir, 0o755); err != nil {
		return nil, err
	}
	dep, err := p.store.CreateDeployment(svc.ProjectID, svc.EnvironmentID, svc.ID, triggerType, commitSHA, commitMsg, "")
	if err != nil {
		return nil, err
	}

	logPath := filepath.Join(p.logsDir, dep.ID+".log")
	_, _ = p.store.DB().Exec(`UPDATE deployments SET log_path = ? WHERE id = ?`, logPath, dep.ID)
	dep.LogPath = logPath

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		_ = p.executeDeployment(ctx, dep, svc)
	}()
	return dep, nil
}

// ExecuteDeploymentSync runs a deployment synchronously (helpful for testing and CLI scripts).
func (p *Pipeline) ExecuteDeploymentSync(ctx context.Context, serviceID, triggerType, commitSHA, commitMsg string) (*db.Deployment, error) {
	svc, err := p.store.GetService(serviceID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.logsDir, 0o755); err != nil {
		return nil, err
	}
	dep, err := p.store.CreateDeployment(svc.ProjectID, svc.EnvironmentID, svc.ID, triggerType, commitSHA, commitMsg, "")
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(p.logsDir, dep.ID+".log")
	_, _ = p.store.DB().Exec(`UPDATE deployments SET log_path = ? WHERE id = ?`, logPath, dep.ID)
	dep.LogPath = logPath

	err = p.executeDeployment(ctx, dep, svc)
	latest, _ := p.store.GetDeployment(dep.ID)
	if latest != nil {
		return latest, err
	}
	return dep, err
}

func (p *Pipeline) executeDeployment(ctx context.Context, dep *db.Deployment, svc *db.Service) error {
	mu := p.lockForService(svc.ID)
	mu.Lock()
	defer mu.Unlock()

	// Refresh latest service record inside service lock.
	freshSvc, err := p.store.GetService(svc.ID)
	if err != nil {
		return err
	}
	svc = freshSvc

	proj, err := p.store.GetProject(svc.ProjectID)
	if err != nil {
		return err
	}

	logger, closeLog := p.newDeployLogger(dep)
	defer closeLog()

	oldContainerID := svc.ActiveContainerID
	hadRunningOld := svc.Status == "running" && oldContainerID != ""

	var ghApp *db.GitHubApp
	var installToken string
	if svc.GitHubAppID != "" {
		ghApp, _ = p.store.GetGitHubApp(svc.GitHubAppID)
	} else {
		ghApp, _ = p.store.GetPrimaryGitHubApp()
	}
	if ghApp != nil && ghApp.Connected && p.ghClient != nil {
		installToken, _ = p.ghClient.InstallationToken(ctx, ghApp)
	}

	fail := func(stage string, cause error) error {
		logger.Writef("[error] 단계 '%s' 실패: %v\n", stage, cause)
		diag := DiagnoseFailure(stage, cause, logger.Tail())
		_ = p.store.FailDeployment(dep.ID, stage, diag.Code, diag.TitleKo, diag.DetailKo)

		// Preserve old healthy service status if a previous container is still serving traffic!
		nextStatus := "failed"
		if hadRunningOld {
			nextStatus = "running"
		}
		_, _ = p.store.UpdateServiceRuntimeStatus(svc.ID, nextStatus, "", "", "", time.Now().UnixNano(), false)
		p.notifyCanvas(svc.ProjectID, svc.EnvironmentID, svc.ID, nextStatus)
		p.reportGitHubStatus(ctx, ghApp, svc, dep.CommitSHA, "failure", diag.TitleKo)
		p.broadcast(LogEvent{
			DeploymentID: dep.ID,
			ServiceID:    svc.ID,
			ProjectID:    svc.ProjectID,
			Stage:        stage,
			Status:       "failed",
			Done:         true,
		})
		return cause
	}

	// Stage 1: Cloning & preparation
	p.setStage(dep, StageCloning, "", "")
	logger.Writef("==> [1/6] 소스 코드 준비 및 빌드 (%s)\n", svc.Name)
	p.reportGitHubStatus(ctx, ghApp, svc, dep.CommitSHA, "pending", "배포를 시작했습니다...")

	// Mark service visually deploying ONLY while keeping oldContainerID untouched until switch.
	if !hadRunningOld {
		_, _ = p.store.UpdateServiceRuntimeStatus(svc.ID, "deploying", "", "", "", time.Now().UnixNano(), false)
		p.notifyCanvas(svc.ProjectID, svc.EnvironmentID, svc.ID, "deploying")
	}

	// Stage 2: Build
	p.setStage(dep, StageBuilding, "", "")
	if p.builder == nil {
		return fail(StageBuilding, fmt.Errorf("build runner is not configured"))
	}
	buildRes, err := p.builder.Build(ctx, build.BuildSpec{
		DeploymentID:   dep.ID,
		ProjectSlug:    proj.Slug,
		ServiceSlug:    svc.Slug,
		RepoFullName:   svc.RepoFullName,
		Branch:         svc.RepoBranch,
		CommitSHA:      dep.CommitSHA,
		RootDir:        svc.RootDir,
		DockerfilePath: svc.DockerfilePath,
		BuildPack:      svc.BuildPack,
		AccessToken:    installToken,
	}, logger)
	if err != nil {
		return fail(StageBuilding, err)
	}
	if buildRes.CommitSHA != "" && dep.CommitSHA == "" {
		dep.CommitSHA = buildRes.CommitSHA
		_, _ = p.store.DB().Exec(`UPDATE deployments SET commit_sha = ?, commit_message = ? WHERE id = ?`, buildRes.CommitSHA, buildRes.CommitMessage, dep.ID)
	}

	// Stage 3: Start candidate container without stopping the live old container.
	p.setStage(dep, StageStartingNew, buildRes.ImageTag, "")
	logger.Writef("==> [3/6] 새 컨테이너(%s) 기동 중...\n", buildRes.ImageTag)

	envMap, err := p.store.ResolveServiceRuntimeEnv(svc.ID)
	if err != nil {
		return fail(StageStartingNew, err)
	}
	if _, hasPort := envMap["PORT"]; !hasPort && svc.Port > 0 {
		envMap["PORT"] = strconv.Itoa(svc.Port)
	}

	shortID := dep.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	candidateName := fmt.Sprintf("ssc-%s-%s-%s", proj.Slug, svc.Slug, shortID)

	candidateInfo, err := p.engine.StartContainer(ctx, docker.ContainerSpec{
		Name:           candidateName,
		Image:          buildRes.ImageTag,
		Env:            formatDockerEnv(envMap),
		ProjectNetwork: proj.NetworkName,
		NetworkAliases: []string{svc.Slug},
		AttachIngress:  true,
		IngressNetwork: p.ingressNetwork,
		RestartPolicy:  "unless-stopped",
		Labels: map[string]string{
			docker.LabelProjectID: proj.ID,
			docker.LabelEnvID:     svc.EnvironmentID,
			docker.LabelServiceID: svc.ID,
			docker.LabelKind:      "app",
		},
	})
	if err != nil {
		return fail(StageStartingNew, err)
	}
	p.setStage(dep, StageHealthchecking, buildRes.ImageTag, candidateInfo.ID)

	// Stage 4: Candidate healthcheck BEFORE cutting over traffic!
	logger.Writef("==> [4/6] 새 컨테이너 헬스체크 확인 중 (최대 %d초)...\n", svc.HealthcheckTimeout)
	if err := p.waitHealthy(ctx, candidateInfo, svc); err != nil {
		logger.Writef("[rollback] 새 컨테이너가 헬스체크에 실패하여 안전하게 제거합니다.\n")
		_ = p.engine.RemoveContainer(ctx, candidateInfo.ID)
		return fail(StageHealthchecking, err)
	}
	logger.Writef("[healthcheck] 새 컨테이너 정상 응답 확인 완료!\n")

	// Stage 5: Atomic Caddy route cutover
	p.setStage(dep, StageSwitching, buildRes.ImageTag, candidateInfo.ID)
	logger.Writef("==> [5/6] 무중단 트래픽 전환 (Caddy /load)...\n")

	observedAt := time.Now().UnixNano()
	if _, err := p.store.UpdateServiceRuntimeStatus(svc.ID, "running", candidateInfo.ID, candidateInfo.Name, buildRes.ImageTag, observedAt, true); err != nil {
		_ = p.engine.RemoveContainer(ctx, candidateInfo.ID)
		return fail(StageSwitching, err)
	}
	if p.router != nil {
		if err := p.router.Sync(ctx); err != nil {
			// Restore previous DB active pointer cleanly if proxy failed.
			if hadRunningOld {
				_, _ = p.store.UpdateServiceRuntimeStatus(svc.ID, "running", oldContainerID, svc.ActiveContainerName, svc.ActiveImage, time.Now().UnixNano(), false)
			}
			_ = p.engine.RemoveContainer(ctx, candidateInfo.ID)
			return fail(StageSwitching, err)
		}
	}

	// Stage 6: Drain & clean up previous container
	p.setStage(dep, StageDrainingOld, buildRes.ImageTag, candidateInfo.ID)
	if oldContainerID != "" && oldContainerID != candidateInfo.ID {
		logger.Writef("==> [6/6] 기존 컨테이너 연결 종료 대기(draining) 후 정리 중...\n")
		select {
		case <-ctx.Done():
		case <-time.After(p.drainDelay):
		}
		_ = p.engine.StopContainer(ctx, oldContainerID, 10*time.Second)
		_ = p.engine.RemoveContainer(ctx, oldContainerID)
	} else {
		logger.Writef("==> [6/6] 첫 배포 완료!\n")
	}

	_ = p.store.CompleteDeployment(dep.ID, buildRes.ImageTag, candidateInfo.ID)
	p.notifyCanvas(svc.ProjectID, svc.EnvironmentID, svc.ID, "running")
	p.reportGitHubStatus(ctx, ghApp, svc, dep.CommitSHA, "success", "무중단 배포가 완료되었습니다.")
	p.broadcast(LogEvent{
		DeploymentID: dep.ID,
		ServiceID:    svc.ID,
		ProjectID:    svc.ProjectID,
		Stage:        StageSucceeded,
		Status:       "succeeded",
		Done:         true,
	})
	return nil
}

func (p *Pipeline) waitHealthy(ctx context.Context, info *docker.ContainerInfo, svc *db.Service) error {
	timeout := time.Duration(svc.HealthcheckTimeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)

	addr := pickTargetAddr(info, svc.Port, p.ingressNetwork)
	for {
		cur, inspectErr := p.engine.InspectContainer(ctx, info.ID)
		if inspectErr == nil {
			if cur.OOMKilled {
				return fmt.Errorf("container oom killed during startup")
			}
			if !cur.Running && time.Since(cur.StartedAt) > 1*time.Second {
				return fmt.Errorf("container exited unexpectedly with code %d", cur.ExitCode)
			}
			if newAddr := pickTargetAddr(cur, svc.Port, p.ingressNetwork); newAddr != "" {
				addr = newAddr
			}
		}

		if addr != "" {
			if err := p.probeFunc(ctx, addr, svc.HealthcheckPath); err == nil {
				return nil
			} else if strings.Contains(err.Error(), "healthcheck_404") && time.Now().Add(2*p.probeInterval).After(deadline) {
				return err
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("healthcheck timeout after %s at %s%s", timeout, addr, svc.HealthcheckPath)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.probeInterval):
		}
	}
}

func pickTargetAddr(info *docker.ContainerInfo, port int, ingressNet string) string {
	if info == nil {
		return ""
	}
	if ip := info.IPByNetwork[ingressNet]; ip != "" {
		return fmt.Sprintf("%s:%d", ip, port)
	}
	for _, ip := range info.IPByNetwork {
		if ip != "" {
			return fmt.Sprintf("%s:%d", ip, port)
		}
	}
	if info.Name != "" {
		return fmt.Sprintf("%s:%d", info.Name, port)
	}
	return ""
}

func defaultHTTPProbe(ctx context.Context, addr, path string) error {
	if strings.HasPrefix(path, "tcp://") || path == "tcp" {
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && path == "/health" {
		return fmt.Errorf("healthcheck_404: %s returned 404", path)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("unexpected healthcheck status %d", resp.StatusCode)
	}
	return nil
}

// StopService stops the running container of a service and re-syncs proxy routes.
func (p *Pipeline) StopService(ctx context.Context, serviceID string) error {
	svc, err := p.store.GetService(serviceID)
	if err != nil {
		return err
	}
	if svc.ActiveContainerID != "" {
		if err := p.engine.StopContainer(ctx, svc.ActiveContainerID, 10*time.Second); err != nil {
			return err
		}
	}
	_, _ = p.store.UpdateServiceRuntimeStatus(svc.ID, "stopped", "", "", "", time.Now().UnixNano(), false)
	if p.router != nil && svc.Kind == "app" {
		_ = p.router.Sync(ctx)
	}
	p.notifyCanvas(svc.ProjectID, svc.EnvironmentID, svc.ID, "stopped")
	return nil
}

// RestartService restarts an existing container (DB or App) with current stored config.
func (p *Pipeline) RestartService(ctx context.Context, serviceID string) error {
	svc, err := p.store.GetService(serviceID)
	if err != nil {
		return err
	}
	if svc.Kind == "database" && svc.ActiveContainerID == "" {
		return p.ProvisionDatabase(ctx, svc)
	}
	if svc.ActiveContainerID != "" {
		_ = p.engine.StopContainer(ctx, svc.ActiveContainerID, 10*time.Second)
		_ = p.engine.RemoveContainer(ctx, svc.ActiveContainerID)
	}

	proj, err := p.store.GetProject(svc.ProjectID)
	if err != nil {
		return err
	}
	if svc.Kind == "database" {
		return p.ProvisionDatabase(ctx, svc)
	}
	if svc.ActiveImage == "" {
		return fmt.Errorf("서비스 이미지가 아직 빌드되지 않았습니다")
	}
	return p.relaunchImage(ctx, proj, svc, svc.ActiveImage, "manual")
}

// Rollback re-launches the image from a prior successful deployment without rebuilding from source.
func (p *Pipeline) Rollback(ctx context.Context, serviceID, targetDeploymentID string) error {
	svc, err := p.store.GetService(serviceID)
	if err != nil {
		return err
	}
	target, err := p.store.GetDeployment(targetDeploymentID)
	if err != nil {
		return err
	}
	if target.ServiceID != svc.ID || target.ImageTag == "" {
		return fmt.Errorf("선택한 배포 버전에 복원 가능한 이미지가 없습니다")
	}
	proj, err := p.store.GetProject(svc.ProjectID)
	if err != nil {
		return err
	}
	return p.relaunchImage(ctx, proj, svc, target.ImageTag, "rollback")
}

func (p *Pipeline) relaunchImage(ctx context.Context, proj *db.Project, svc *db.Service, imageTag, trigger string) error {
	dep, err := p.store.CreateDeployment(svc.ProjectID, svc.EnvironmentID, svc.ID, trigger, "", "Rollback / Restart "+imageTag, "")
	if err != nil {
		return err
	}
	logPath := filepath.Join(p.logsDir, dep.ID+".log")
	_, _ = p.store.DB().Exec(`UPDATE deployments SET log_path = ? WHERE id = ?`, logPath, dep.ID)
	dep.LogPath = logPath

	logger, closeLog := p.newDeployLogger(dep)
	defer closeLog()

	oldContainerID := svc.ActiveContainerID
	envMap, err := p.store.ResolveServiceRuntimeEnv(svc.ID)
	if err != nil {
		return err
	}
	if _, hasPort := envMap["PORT"]; !hasPort && svc.Port > 0 {
		envMap["PORT"] = strconv.Itoa(svc.Port)
	}

	shortID := dep.ID[:8]
	candidateName := fmt.Sprintf("ssc-%s-%s-%s", proj.Slug, svc.Slug, shortID)
	p.setStage(dep, StageStartingNew, imageTag, "")
	logger.Writef("==> [복원] 이미지 %s 로 새 컨테이너를 기동합니다...\n", imageTag)

	info, err := p.engine.StartContainer(ctx, docker.ContainerSpec{
		Name:           candidateName,
		Image:          imageTag,
		Env:            formatDockerEnv(envMap),
		ProjectNetwork: proj.NetworkName,
		NetworkAliases: []string{svc.Slug},
		AttachIngress:  true,
		IngressNetwork: p.ingressNetwork,
		RestartPolicy:  "unless-stopped",
		Labels: map[string]string{
			docker.LabelProjectID: proj.ID,
			docker.LabelEnvID:     svc.EnvironmentID,
			docker.LabelServiceID: svc.ID,
			docker.LabelKind:      "app",
		},
	})
	if err != nil {
		diag := DiagnoseFailure(StageStartingNew, err, logger.Tail())
		_ = p.store.FailDeployment(dep.ID, StageStartingNew, diag.Code, diag.TitleKo, diag.DetailKo)
		return err
	}
	p.setStage(dep, StageHealthchecking, imageTag, info.ID)
	if err := p.waitHealthy(ctx, info, svc); err != nil {
		_ = p.engine.RemoveContainer(ctx, info.ID)
		diag := DiagnoseFailure(StageHealthchecking, err, logger.Tail())
		_ = p.store.FailDeployment(dep.ID, StageHealthchecking, diag.Code, diag.TitleKo, diag.DetailKo)
		return err
	}

	p.setStage(dep, StageSwitching, imageTag, info.ID)
	_, _ = p.store.UpdateServiceRuntimeStatus(svc.ID, "running", info.ID, info.Name, imageTag, time.Now().UnixNano(), true)
	if p.router != nil {
		_ = p.router.Sync(ctx)
	}
	if oldContainerID != "" && oldContainerID != info.ID {
		time.Sleep(p.drainDelay)
		_ = p.engine.StopContainer(ctx, oldContainerID, 10*time.Second)
		_ = p.engine.RemoveContainer(ctx, oldContainerID)
	}
	_ = p.store.CompleteDeployment(dep.ID, imageTag, info.ID)
	p.notifyCanvas(svc.ProjectID, svc.EnvironmentID, svc.ID, "running")
	return nil
}

func (p *Pipeline) setStage(dep *db.Deployment, stage, imageTag, containerID string) {
	dep.Stage = stage
	_ = p.store.UpdateDeploymentStage(dep.ID, stage, imageTag, containerID)
	p.broadcast(LogEvent{
		DeploymentID: dep.ID,
		ServiceID:    dep.ServiceID,
		ProjectID:    dep.ProjectID,
		Stage:        stage,
	})
}

func (p *Pipeline) notifyCanvas(projectID, envID, serviceID, status string) {
	if p.onCanvasChange != nil {
		p.onCanvasChange(projectID, envID, serviceID, status)
	}
}

func (p *Pipeline) reportGitHubStatus(ctx context.Context, ghApp *db.GitHubApp, svc *db.Service, sha, state, desc string) {
	if p.ghClient == nil || ghApp == nil || svc.RepoFullName == "" || sha == "" {
		return
	}
	_ = p.ghClient.ReportCommitStatus(ctx, ghApp, svc.RepoFullName, sha, state, "", desc)
}

func (p *Pipeline) broadcast(evt LogEvent) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, ch := range p.subscribers[evt.DeploymentID] {
		select {
		case ch <- evt:
		default:
		}
	}
}

func (p *Pipeline) lockForService(serviceID string) *sync.Mutex {
	p.svcLocksMu.Lock()
	defer p.svcLocksMu.Unlock()
	m, ok := p.svcLocks[serviceID]
	if !ok {
		m = &sync.Mutex{}
		p.svcLocks[serviceID] = m
	}
	return m
}

func formatDockerEnv(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+vars[k])
	}
	return out
}

func buildDatabaseContainerSpec(proj *db.Project, svc *db.Service) (docker.ContainerSpec, []db.SecretInput, error) {
	pw, err := crypto.RandomHex(16)
	if err != nil {
		return docker.ContainerSpec{}, nil, err
	}

	volName := svc.VolumeName
	if volName == "" {
		volName = fmt.Sprintf("ssc-vol-%s-%s", proj.Slug, svc.Slug)
	}
	containerName := fmt.Sprintf("ssc-%s-%s", proj.Slug, svc.Slug)

	var targetDir, imageName string
	var secrets []db.SecretInput

	switch strings.ToLower(svc.DBType) {
	case "postgres", "":
		imageName = svc.DBImage
		if imageName == "" {
			imageName = "postgres:16-alpine"
		}
		targetDir = "/var/lib/postgresql/data"
		connURL := fmt.Sprintf("postgresql://app:%s@%s:5432/app", pw, svc.Slug)
		secrets = []db.SecretInput{
			{Key: "POSTGRES_USER", Value: "app", IsSystem: true},
			{Key: "POSTGRES_PASSWORD", Value: pw, IsSystem: true},
			{Key: "POSTGRES_DB", Value: "app", IsSystem: true},
			{Key: "DATABASE_URL", Value: connURL, IsSystem: true},
			{Key: "CONNECTION_URL", Value: connURL, IsSystem: true},
		}
	case "redis":
		imageName = svc.DBImage
		if imageName == "" {
			imageName = "redis:7-alpine"
		}
		targetDir = "/data"
		connURL := fmt.Sprintf("redis://:%s@%s:6379", pw, svc.Slug)
		secrets = []db.SecretInput{
			{Key: "REDIS_PASSWORD", Value: pw, IsSystem: true},
			{Key: "REDIS_URL", Value: connURL, IsSystem: true},
			{Key: "CONNECTION_URL", Value: connURL, IsSystem: true},
		}
	case "mysql":
		imageName = svc.DBImage
		if imageName == "" {
			imageName = "mysql:8.4"
		}
		targetDir = "/var/lib/mysql"
		connURL := fmt.Sprintf("mysql://app:%s@%s:3306/app", pw, svc.Slug)
		secrets = []db.SecretInput{
			{Key: "MYSQL_ROOT_PASSWORD", Value: pw, IsSystem: true},
			{Key: "MYSQL_USER", Value: "app", IsSystem: true},
			{Key: "MYSQL_PASSWORD", Value: pw, IsSystem: true},
			{Key: "MYSQL_DATABASE", Value: "app", IsSystem: true},
			{Key: "DATABASE_URL", Value: connURL, IsSystem: true},
			{Key: "CONNECTION_URL", Value: connURL, IsSystem: true},
		}
	default:
		return docker.ContainerSpec{}, nil, fmt.Errorf("unsupported database type %q", svc.DBType)
	}

	var cmd []string
	if strings.ToLower(svc.DBType) == "redis" {
		cmd = []string{"redis-server", "--requirepass", pw, "--appendonly", "yes"}
	}

	return docker.ContainerSpec{
		Name:           containerName,
		Image:          imageName,
		Cmd:            cmd,
		ProjectNetwork: proj.NetworkName,
		NetworkAliases: []string{svc.Slug},
		AttachIngress:  false, // Databases are strictly isolated inside the project network.
		RestartPolicy:  "unless-stopped",
		Labels: map[string]string{
			docker.LabelProjectID: proj.ID,
			docker.LabelEnvID:     svc.EnvironmentID,
			docker.LabelServiceID: svc.ID,
			docker.LabelKind:      "database",
		},
		Mounts: []docker.MountSpec{
			{Type: "volume", Source: volName, Target: targetDir},
		},
	}, secrets, nil
}

type deployLogger struct {
	mu       sync.Mutex
	file     *os.File
	tailBuf  []string
	dep      *db.Deployment
	pipeline *Pipeline
	partial  strings.Builder
}

func (p *Pipeline) newDeployLogger(dep *db.Deployment) (*deployLogger, func()) {
	f, _ := os.OpenFile(dep.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	dl := &deployLogger{file: f, dep: dep, pipeline: p}
	return dl, func() {
		dl.mu.Lock()
		defer dl.mu.Unlock()
		if dl.partial.Len() > 0 {
			dl.emitLineLocked(dl.partial.String())
			dl.partial.Reset()
		}
		if dl.file != nil {
			_ = dl.file.Close()
		}
	}
}

func (l *deployLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		_, _ = l.file.Write(p)
	}
	l.partial.Write(p)
	text := l.partial.String()
	for {
		idx := strings.IndexByte(text, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(text[:idx], "\r")
		l.emitLineLocked(line)
		text = text[idx+1:]
	}
	l.partial.Reset()
	l.partial.WriteString(text)
	return len(p), nil
}

func (l *deployLogger) Writef(format string, args ...any) {
	_, _ = fmt.Fprintf(l, format, args...)
}

func (l *deployLogger) emitLineLocked(line string) {
	l.tailBuf = append(l.tailBuf, line)
	if len(l.tailBuf) > 50 {
		l.tailBuf = l.tailBuf[len(l.tailBuf)-50:]
	}
	l.pipeline.broadcast(LogEvent{
		DeploymentID: l.dep.ID,
		ServiceID:    l.dep.ServiceID,
		ProjectID:    l.dep.ProjectID,
		Stage:        l.dep.Stage,
		Line:         line,
	})
}

func (l *deployLogger) Tail() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.tailBuf, "\n")
}
