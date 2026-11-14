package deploy_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/build"
	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/deploy"
	"github.com/gachon-star-want/simple-server-control/internal/docker"
)

type fakeEngine struct {
	mu         sync.Mutex
	seq        int
	started    []docker.ContainerSpec
	removedIDs []string
	stoppedIDs []string
	ops        []string // ordered "start:<image>" / "stop:<id>" log
}

func (f *fakeEngine) Ping(context.Context) error                    { return nil }
func (f *fakeEngine) EnsureNetwork(context.Context, string) error   { return nil }
func (f *fakeEngine) RemoveNetwork(context.Context, string) error   { return nil }
func (f *fakeEngine) EnsureVolume(context.Context, string) error    { return nil }
func (f *fakeEngine) PullImage(context.Context, string, io.Writer) error { return nil }
func (f *fakeEngine) StartContainer(_ context.Context, spec docker.ContainerSpec) (*docker.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("cid-%d", f.seq)
	f.started = append(f.started, spec)
	f.ops = append(f.ops, "start:"+spec.Image)
	return &docker.ContainerInfo{
		ID:          id,
		Name:        spec.Name,
		Running:     true,
		Status:      "running",
		StartedAt:   time.Now(),
		IPByNetwork: map[string]string{"ssc-ingress": fmt.Sprintf("10.0.1.%d", f.seq)},
	}, nil
}
func (f *fakeEngine) StopContainer(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stoppedIDs = append(f.stoppedIDs, id)
	f.ops = append(f.ops, "stop:"+id)
	return nil
}
func (f *fakeEngine) RemoveContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedIDs = append(f.removedIDs, id)
	return nil
}
func (f *fakeEngine) InspectContainer(_ context.Context, id string) (*docker.ContainerInfo, error) {
	return &docker.ContainerInfo{
		ID:          id,
		Name:        "mock-" + id,
		Running:     true,
		Status:      "running",
		StartedAt:   time.Now(),
		IPByNetwork: map[string]string{"ssc-ingress": "10.0.1.99"},
	}, nil
}
func (f *fakeEngine) ListManagedContainers(context.Context, string) ([]docker.ContainerInfo, error) {
	return nil, nil
}
func (f *fakeEngine) StreamLogs(context.Context, string, string, bool) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (f *fakeEngine) OneShotStats(context.Context, string) (*docker.ContainerStats, error) {
	return &docker.ContainerStats{}, nil
}
func (f *fakeEngine) OpenExec(context.Context, string, []string, uint, uint) (*docker.ExecSession, error) {
	return nil, fmt.Errorf("not supported")
}
func (f *fakeEngine) WatchEvents(context.Context, func(docker.EngineEvent)) error { return nil }
func (f *fakeEngine) Close() error                                                { return nil }

type fakeRouter struct {
	mu    sync.Mutex
	syncs int
	fail  bool
}

func (r *fakeRouter) EnsureRunning(context.Context) error { return nil }
func (r *fakeRouter) Sync(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return fmt.Errorf("caddy sync failed")
	}
	r.syncs++
	return nil
}
func (r *fakeRouter) AutoDomain(svc, proj string) string { return svc + "-" + proj + ".sslip.io" }
func (r *fakeRouter) VerifyDNS(context.Context, string) (bool, []string, string, string) {
	return true, nil, "1.2.3.4", "ok"
}

type fakeBuilder struct {
	tag string
	err error
}

func (b *fakeBuilder) Build(_ context.Context, spec build.BuildSpec, logOut io.Writer) (*build.BuildResult, error) {
	_, _ = fmt.Fprintln(logOut, "mock building "+spec.RepoFullName)
	if b.err != nil {
		return nil, b.err
	}
	return &build.BuildResult{
		CommitSHA:     "deadbee",
		CommitMessage: "deploy commit",
		ImageTag:      b.tag,
	}, nil
}

func TestZeroDowntimeSwapAndFailurePreservesOldContainer(t *testing.T) {
	vault, _ := crypto.NewVaultFromKey(bytes.Repeat([]byte{0x55}, 32))
	tmp := t.TempDir()
	store, err := db.Open(filepath.Join(tmp, "deploy.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	proj, env, _ := store.CreateProject("PleumRouter", "")
	dbSvc, _ := store.CreateService(db.CreateServiceInput{
		ProjectID:     proj.ID,
		EnvironmentID: env.ID,
		Name:          "postgres",
		Kind:          "database",
		DBType:        "postgres",
	})
	appSvc, _ := store.CreateService(db.CreateServiceInput{
		ProjectID:     proj.ID,
		EnvironmentID: env.ID,
		Name:          "api",
		Kind:          "app",
		Port:          8080,
		RepoFullName:  "gachon-star-want/pleumrouter",
		HealthcheckTimeout: 1,
	})

	eng := &fakeEngine{}
	rtr := &fakeRouter{}
	bldr := &fakeBuilder{tag: "ssc-pleumrouter-api:v1"}
	pipe := deploy.NewPipeline(store, eng, rtr, bldr, nil, filepath.Join(tmp, "logs"), "ssc-ingress", nil)
	pipe.SetTimings(5*time.Millisecond, 5*time.Millisecond)
	pipe.SetProbeFunc(func(context.Context, string, string) error { return nil })

	// 1. Provision isolated Postgres (must NEVER attach to ssc-ingress or host ports).
	if err := pipe.ProvisionDatabase(context.Background(), dbSvc); err != nil {
		t.Fatalf("ProvisionDatabase: %v", err)
	}
	if len(eng.started) != 1 || eng.started[0].AttachIngress || len(eng.started[0].Ports) != 0 {
		t.Fatalf("database container violated network/port isolation: %+v", eng.started[0])
	}

	// 2. First green deploy succeeds -> old=nil, active container cid-2.
	dep1, err := pipe.ExecuteDeploymentSync(context.Background(), appSvc.ID, "manual", "", "")
	if err != nil || dep1.Status != "succeeded" {
		t.Fatalf("first deploy failed: %+v err=%v", dep1, err)
	}
	curSvc, _ := store.GetService(appSvc.ID)
	firstCID := curSvc.ActiveContainerID
	if firstCID != "cid-2" || curSvc.Status != "running" {
		t.Fatalf("unexpected active service after deploy 1: %+v", curSvc)
	}

	// 3. Second deploy FAILS during healthcheck -> candidate cid-3 MUST be removed,
	// cid-2 MUST stay alive & untouched, service status remains "running"!
	bldr.tag = "ssc-pleumrouter-api:v2-broken"
	pipe.SetProbeFunc(func(context.Context, string, string) error {
		return fmt.Errorf("healthcheck_404: /health returned 404")
	})
	dep2, err := pipe.ExecuteDeploymentSync(context.Background(), appSvc.ID, "webhook", "abc123", "broken commit")
	if err == nil || dep2.Status != "failed" || dep2.ErrorCode != "HEALTHCHECK_404" {
		t.Fatalf("expected HEALTHCHECK_404 failure, got dep=%+v err=%v", dep2, err)
	}
	curSvc, _ = store.GetService(appSvc.ID)
	if curSvc.ActiveContainerID != firstCID || curSvc.Status != "running" {
		t.Fatalf("failed deploy corrupted live active container! got %+v", curSvc)
	}
	if !slices.Contains(eng.removedIDs, "cid-3") || slices.Contains(eng.removedIDs, firstCID) {
		t.Fatalf("expected failed candidate cid-3 removed and live cid-2 kept, removed=%v", eng.removedIDs)
	}

	// 4. Third deploy SUCCEEDS -> switches Caddy to cid-4 and drains+removes cid-2!
	bldr.tag = "ssc-pleumrouter-api:v3-good"
	pipe.SetProbeFunc(func(context.Context, string, string) error { return nil })
	dep3, err := pipe.ExecuteDeploymentSync(context.Background(), appSvc.ID, "manual", "", "")
	if err != nil || dep3.Status != "succeeded" {
		t.Fatalf("third deploy failed: %+v err=%v", dep3, err)
	}
	curSvc, _ = store.GetService(appSvc.ID)
	if curSvc.ActiveContainerID != "cid-4" || !slices.Contains(eng.removedIDs, firstCID) {
		t.Fatalf("expected zero-downtime swap to cid-4 and removal of %s, got svc=%+v removed=%v", firstCID, curSvc, eng.removedIDs)
	}
	if rtr.syncs < 2 {
		t.Fatalf("expected caddy syncs on successful deployments, got %d", rtr.syncs)
	}
}

func TestDataFolderMountsVolumeAndStopsOldFirst(t *testing.T) {
	vault, _ := crypto.NewVaultFromKey(bytes.Repeat([]byte{0x56}, 32))
	tmp := t.TempDir()
	store, err := db.Open(filepath.Join(tmp, "deploy.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	proj, env, _ := store.CreateProject("Mulgyeol", "")
	appSvc, err := store.CreateService(db.CreateServiceInput{
		ProjectID: proj.ID, EnvironmentID: env.ID, Name: "game", Kind: "app",
		Port: 2567, RepoFullName: "x/mulgyeol", HealthcheckTimeout: 1, DataPath: "/data/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if appSvc.DataPath != "/data" || appSvc.VolumeName != "ssc-vol-mulgyeol-game" {
		t.Fatalf("data path/volume not normalized: %q %q", appSvc.DataPath, appSvc.VolumeName)
	}
	if _, err := store.CreateService(db.CreateServiceInput{ProjectID: proj.ID, EnvironmentID: env.ID, Name: "bad", Kind: "app", DataPath: "data"}); err == nil {
		t.Fatal("relative data path must be rejected")
	}

	eng := &fakeEngine{}
	bldr := &fakeBuilder{tag: "img:v1"}
	pipe := deploy.NewPipeline(store, eng, &fakeRouter{}, bldr, nil, filepath.Join(tmp, "logs"), "ssc-ingress", nil)
	pipe.SetTimings(time.Millisecond, time.Millisecond)
	// The container being probed is always the most recently started one.
	pipe.SetProbeFunc(func(context.Context, string, string) error {
		eng.mu.Lock()
		img := eng.started[len(eng.started)-1].Image
		eng.mu.Unlock()
		if img == "img:broken" {
			return fmt.Errorf("healthcheck_404: /health returned 404")
		}
		return nil
	})
	deployOK := func(tag string) {
		t.Helper()
		bldr.tag = tag
		if dep, err := pipe.ExecuteDeploymentSync(context.Background(), appSvc.ID, "manual", "", ""); err != nil || dep.Status != "succeeded" {
			t.Fatalf("deploy %s failed: %+v %v", tag, dep, err)
		}
	}

	// 1. First deploy mounts the named volume at /data.
	deployOK("img:v1")
	m := eng.started[0].Mounts
	if len(m) != 1 || m[0].Type != "volume" || m[0].Source != "ssc-vol-mulgyeol-game" || m[0].Target != "/data" {
		t.Fatalf("app container missing data volume: %+v", m)
	}

	// 2. Redeploy stops the old container BEFORE the new one starts.
	eng.ops = nil
	deployOK("img:v2")
	if !slices.Equal(eng.ops[:2], []string{"stop:cid-1", "start:img:v2"}) {
		t.Fatalf("expected stop-then-start, got %v", eng.ops)
	}

	// 3. A broken release is rolled back to the previous image, on the same volume.
	bldr.tag = "img:broken"
	dep, err := pipe.ExecuteDeploymentSync(context.Background(), appSvc.ID, "manual", "", "")
	if err == nil || dep.Status != "failed" {
		t.Fatalf("broken deploy should fail: %+v", dep)
	}
	last := eng.started[len(eng.started)-1]
	if last.Image != "img:v2" || len(last.Mounts) != 1 {
		t.Fatalf("expected restore from img:v2 with volume, got %+v", last)
	}
	cur, _ := store.GetService(appSvc.ID)
	if cur.Status != "running" || cur.ActiveImage != "img:v2" || cur.ActiveContainerID != "cid-4" {
		t.Fatalf("service not restored: %+v", cur)
	}
	if !slices.Contains(eng.removedIDs, "cid-2") || !slices.Contains(eng.removedIDs, "cid-3") {
		t.Fatalf("old stopped container and broken candidate should be removed: %v", eng.removedIDs)
	}
}
