//go:build e2e

// Real-Docker check of the data-folder flow with a Mulgyeol-like app (WebSocket + SQLite on /data),
// served through the real ssc-caddy container on host port 80.
// Run: SSC_E2E_IMAGE=ssc-e2e-mulgyeol go test -tags e2e -run TestE2E -v ./internal/deploy/
// Needs local images <image>:v1, :v2 and :broken (a /health that returns 500), and free ports 80/443/2019.
package deploy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gachon-star-want/simple-server-control/internal/build"
	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/deploy"
	"github.com/gachon-star-want/simple-server-control/internal/docker"
	"github.com/gachon-star-want/simple-server-control/internal/proxy"
)

type tagBuilder struct{ tag string }

func (b *tagBuilder) Build(_ context.Context, _ build.BuildSpec, w io.Writer) (*build.BuildResult, error) {
	_, _ = fmt.Fprintln(w, "using prebuilt image "+b.tag)
	return &build.BuildResult{ImageTag: b.tag}, nil
}

func TestE2EDataFolderSurvivesRedeployThroughCaddy(t *testing.T) {
	image := os.Getenv("SSC_E2E_IMAGE")
	if image == "" {
		t.Skip("set SSC_E2E_IMAGE")
	}
	ctx := context.Background()
	eng, err := docker.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	tmp := t.TempDir()
	vault, _ := crypto.NewVaultFromKey(bytes.Repeat([]byte{0x61}, 32))
	store, err := db.Open(filepath.Join(tmp, "e2e.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	proj, env, _ := store.CreateProject("Mulgyeol E2E", "")
	svc, err := store.CreateService(db.CreateServiceInput{
		ProjectID: proj.ID, EnvironmentID: env.ID, Name: "game", Kind: "app",
		Port: 2567, HealthcheckPath: "/health", HealthcheckTimeout: 20, DataPath: "/data",
	})
	if err != nil {
		t.Fatal(err)
	}
	const host = "game.localtest.me" // public wildcard DNS → 127.0.0.1
	if _, err := store.CreateDomain(svc.ID, host, false, false, false); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=ssc.project_id="+proj.ID).Output()
		ids := strings.Fields(string(out))
		_ = exec.Command("docker", append([]string{"rm", "-f", "ssc-caddy", "ssc-e2e-probe"}, ids...)...).Run()
		_ = exec.Command("docker", "volume", "rm", svc.VolumeName).Run()
		_ = exec.Command("docker", "network", "rm", proj.NetworkName, "ssc-ingress").Run()
	})

	rtr := proxy.NewCaddyRouter(store, eng, "", "ssc-ingress", tmp, "")
	if err := rtr.EnsureRunning(ctx); err != nil {
		t.Fatalf("caddy: %v", err)
	}
	// Container IPs are not reachable from a macOS host, so probe from a helper on ssc-ingress.
	if out, err := exec.Command("docker", "run", "-d", "--name", "ssc-e2e-probe", "--network", "ssc-ingress", image+":v1", "sleep", "3600").CombinedOutput(); err != nil {
		t.Fatalf("probe helper: %v %s", err, out)
	}

	bldr := &tagBuilder{}
	pipe := deploy.NewPipeline(store, eng, rtr, bldr, nil, filepath.Join(tmp, "logs"), "ssc-ingress", nil)
	pipe.SetTimings(time.Second, time.Second)
	pipe.SetProbeFunc(func(ctx context.Context, addr, path string) error {
		return exec.CommandContext(ctx, "docker", "exec", "ssc-e2e-probe", "wget", "-q", "-O-", "http://"+addr+path).Run()
	})

	visit := func() (version string, visits int) {
		t.Helper()
		var last error
		for i := 0; i < 20; i++ { // Caddy reload + container start can take a moment
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			c, _, err := websocket.Dial(cctx, "ws://"+host+"/", nil)
			if err == nil {
				_, msg, rerr := c.Read(cctx)
				_ = c.Close(websocket.StatusNormalClosure, "")
				cancel()
				var got struct {
					Version string `json:"version"`
					Visits  int    `json:"visits"`
				}
				if rerr == nil && json.Unmarshal(msg, &got) == nil {
					return got.Version, got.Visits
				}
				err = rerr
			}
			cancel()
			last = err
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("websocket via caddy failed: %v", last)
		return "", 0
	}
	deployTag := func(tag string) (*db.Deployment, error) {
		bldr.tag = image + ":" + tag
		return pipe.ExecuteDeploymentSync(ctx, svc.ID, "manual", "", "")
	}

	if dep, err := deployTag("v1"); err != nil {
		t.Fatalf("deploy v1: %v %+v", err, dep)
	}
	visit()
	if v, n := visit(); v != "v1" || n != 2 {
		t.Fatalf("after v1: version=%s visits=%d, want v1/2", v, n)
	}

	if dep, err := deployTag("v2"); err != nil {
		t.Fatalf("deploy v2: %v %+v", err, dep)
	}
	if v, n := visit(); v != "v2" || n != 3 {
		t.Fatalf("after redeploy: version=%s visits=%d, want v2/3 (data must survive)", v, n)
	}

	dep, err := deployTag("broken")
	if err == nil {
		t.Fatalf("broken deploy should fail: %+v", dep)
	}
	if v, n := visit(); v != "v2" || n != 4 {
		t.Fatalf("after failed deploy: version=%s visits=%d, want restored v2/4", v, n)
	}
	logBytes, _ := os.ReadFile(dep.LogPath)
	t.Logf("broken deploy log:\n%s", logBytes)
}
