// Package build executes isolated source cloning and OCI image builds via Railpack + BuildKit or Dockerfile.
package build

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/docker"
)

const (
	// BuildKitContainerName is the shared BuildKit daemon container required by Railpack.
	BuildKitContainerName = "ssc-buildkit"
	// BuildKitImage is the upstream BuildKit container image.
	BuildKitImage = "moby/buildkit:latest"
)

// BuildSpec defines parameters for building an app image from GitHub source.
type BuildSpec struct {
	DeploymentID   string
	ProjectSlug    string
	ServiceSlug    string
	RepoFullName   string
	Branch         string
	CommitSHA      string
	RootDir        string
	DockerfilePath string
	BuildPack      string // "auto" | "railpack" | "dockerfile"
	AccessToken    string
	BuildEnv       map[string]string
}

// BuildResult returns the checked-out commit metadata and built Docker image tag.
type BuildResult struct {
	CommitSHA     string
	CommitMessage string
	ImageTag      string
}

// Runner abstracts repository cloning and container image building.
type Runner interface {
	Build(ctx context.Context, spec BuildSpec, logOut io.Writer) (*BuildResult, error)
}

// ContainerBuilder clones repositories cleanly without leaking tokens and builds via Dockerfile or Railpack.
type ContainerBuilder struct {
	engine       docker.Engine
	reposDir     string
	buildkitAddr string
}

// NewContainerBuilder creates a builder storing temporary checkouts under reposDir.
func NewContainerBuilder(engine docker.Engine, reposDir, buildkitAddr string) *ContainerBuilder {
	if buildkitAddr == "" {
		buildkitAddr = "tcp://127.0.0.1:1234"
	}
	return &ContainerBuilder{
		engine:       engine,
		reposDir:     reposDir,
		buildkitAddr: buildkitAddr,
	}
}

// Build clones spec.RepoFullName at spec.Branch and produces a local Docker image ready to start.
func (b *ContainerBuilder) Build(ctx context.Context, spec BuildSpec, logOut io.Writer) (*BuildResult, error) {
	if spec.RepoFullName == "" {
		return nil, fmt.Errorf("git clone: repository is not configured")
	}
	if err := os.MkdirAll(b.reposDir, 0o755); err != nil {
		return nil, err
	}

	workDir, err := os.MkdirTemp(b.reposDir, "build-"+spec.DeploymentID+"-*")
	if err != nil {
		return nil, fmt.Errorf("create build workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	if err := cloneRepo(ctx, spec, workDir, logOut); err != nil {
		return nil, err
	}

	sha, msg := readGitHead(ctx, workDir)
	if sha == "" {
		sha = spec.CommitSHA
	}

	subDir := workDir
	if cleaned := strings.TrimSpace(spec.RootDir); cleaned != "" && cleaned != "." {
		joined := filepath.Clean(filepath.Join(workDir, cleaned))
		if !strings.HasPrefix(joined, workDir) {
			return nil, fmt.Errorf("invalid root_dir path: escapes repository root")
		}
		subDir = joined
	}

	shortID := spec.DeploymentID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	imageTag := fmt.Sprintf("ssc-%s-%s:%s", spec.ProjectSlug, spec.ServiceSlug, shortID)

	useDockerfile, dfPath := resolveDockerfile(subDir, spec.BuildPack, spec.DockerfilePath)
	if useDockerfile {
		_, _ = fmt.Fprintf(logOut, "[build] Dockerfile 감지됨 (%s) — Docker BuildKit으로 빌드합니다.\n", filepath.Base(dfPath))
		if err := b.buildWithDocker(ctx, subDir, dfPath, imageTag, logOut); err != nil {
			return nil, err
		}
	} else {
		_, _ = fmt.Fprintf(logOut, "[build] Railpack 자동 감지로 %s 런타임 빌드를 시작합니다.\n", spec.ServiceSlug)
		if err := b.ensureBuildKit(ctx, logOut); err != nil {
			return nil, err
		}
		if err := b.buildWithRailpack(ctx, subDir, imageTag, logOut); err != nil {
			return nil, err
		}
	}

	return &BuildResult{
		CommitSHA:     sha,
		CommitMessage: msg,
		ImageTag:      imageTag,
	}, nil
}

func cloneRepo(ctx context.Context, spec BuildSpec, workDir string, logOut io.Writer) error {
	branch := spec.Branch
	if branch == "" {
		branch = "main"
	}
	repoURL := fmt.Sprintf("https://github.com/%s.git", strings.Trim(spec.RepoFullName, "/"))
	_, _ = fmt.Fprintf(logOut, "[clone] %s (%s 브랜치) 소스 코드를 가져오는 중...\n", spec.RepoFullName, branch)

	args := []string{"clone", "--depth", "1", "--branch", branch, repoURL, workDir}
	cmd := exec.CommandContext(ctx, "git", args...)
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	if spec.AccessToken != "" {
		askpassPath, cleanup, err := writeGitAskpassScript(spec.AccessToken)
		if err != nil {
			return fmt.Errorf("git auth helper: %w", err)
		}
		defer cleanup()
		env = append(env, "GIT_ASKPASS="+askpassPath)
	}
	cmd.Env = env

	if err := streamCommandOutput(cmd, logOut); err != nil {
		return fmt.Errorf("git clone %s: %w", spec.RepoFullName, err)
	}

	if spec.CommitSHA != "" {
		chk := exec.CommandContext(ctx, "git", "-C", workDir, "checkout", spec.CommitSHA)
		// Shallow clone may only have branch HEAD; ignore if checkout matches HEAD cleanly.
		_ = chk.Run()
	}
	return nil
}

func writeGitAskpassScript(token string) (string, func(), error) {
	f, err := os.CreateTemp("", "ssc-askpass-*")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n*Username*) echo \"x-access-token\" ;;\n*Password*) echo %q ;;\nesac\n", token)
	if _, err := f.WriteString(script); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", nil, err
	}
	_ = f.Close()
	if err := os.Chmod(path, 0o700); err != nil {
		_ = os.Remove(path)
		return "", nil, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func readGitHead(ctx context.Context, dir string) (string, string) {
	shaOut, _ := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	msgOut, _ := exec.CommandContext(ctx, "git", "-C", dir, "log", "-1", "--pretty=%s").Output()
	return strings.TrimSpace(string(shaOut)), strings.TrimSpace(string(msgOut))
}

func resolveDockerfile(subDir, buildPack, explicitDF string) (bool, string) {
	if explicitDF != "" {
		candidate := filepath.Clean(filepath.Join(subDir, explicitDF))
		if _, err := os.Stat(candidate); err == nil {
			return true, candidate
		}
	}
	if buildPack == "railpack" {
		return false, ""
	}
	defaultDF := filepath.Join(subDir, "Dockerfile")
	if _, err := os.Stat(defaultDF); err == nil {
		return true, defaultDF
	}
	if buildPack == "dockerfile" {
		return true, defaultDF
	}
	return false, ""
}

func (b *ContainerBuilder) buildWithDocker(ctx context.Context, dir, dockerfilePath, imageTag string, logOut io.Writer) error {
	cmd := exec.CommandContext(ctx, "docker", "build", "-f", dockerfilePath, "-t", imageTag, dir)
	cmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	if err := streamCommandOutput(cmd, logOut); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

func (b *ContainerBuilder) ensureBuildKit(ctx context.Context, logOut io.Writer) error {
	if b.engine == nil {
		return nil
	}
	info, err := b.engine.InspectContainer(ctx, BuildKitContainerName)
	if err == nil && info.Running {
		return nil
	}
	if err == nil {
		_ = b.engine.RemoveContainer(ctx, BuildKitContainerName)
	}

	_, _ = fmt.Fprintln(logOut, "[build] BuildKit 데몬 컨테이너를 준비하는 중...")
	_, err = b.engine.StartContainer(ctx, docker.ContainerSpec{
		Name:          BuildKitContainerName,
		Image:         BuildKitImage,
		Cmd:           []string{"--addr", "tcp://0.0.0.0:1234"},
		Privileged:    true,
		RestartPolicy: "unless-stopped",
		Labels: map[string]string{
			docker.LabelKind: "buildkit",
		},
		Ports: []docker.PortBinding{
			{HostIP: "127.0.0.1", HostPort: "1234", ContainerPort: "1234/tcp"},
		},
	})
	if err != nil {
		return fmt.Errorf("start buildkit daemon: %w", err)
	}
	time.Sleep(1500 * time.Millisecond)
	return nil
}

func (b *ContainerBuilder) buildWithRailpack(ctx context.Context, dir, imageTag string, logOut io.Writer) error {
	if _, err := exec.LookPath("railpack"); err != nil {
		return fmt.Errorf("railpack binary missing: install script or Dockerfile required: %w", err)
	}
	cmd := exec.CommandContext(ctx, "railpack", "build", dir, "--name", imageTag)
	cmd.Env = append(os.Environ(), "BUILDKIT_HOST="+b.buildkitAddr)
	if err := streamCommandOutput(cmd, logOut); err != nil {
		return fmt.Errorf("railpack build failed: %w", err)
	}
	return nil
}

func streamCommandOutput(cmd *exec.Cmd, out io.Writer) error {
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			_, _ = fmt.Fprintln(out, scanner.Text())
		}
	}()

	waitErr := cmd.Wait()
	_ = pw.Close()
	<-done
	_ = pr.Close()
	return waitErr
}
