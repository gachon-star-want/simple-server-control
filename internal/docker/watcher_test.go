package docker_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/gachon-star-want/simple-server-control/internal/crypto"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/docker"
)

func TestWatcherIgnoresReplacedContainerEvents(t *testing.T) {
	vault, err := crypto.NewVaultFromKey(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "watcher.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	proj, env, err := store.CreateProject("WatchApp", "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := store.CreateService(db.CreateServiceInput{
		ProjectID:     proj.ID,
		EnvironmentID: env.ID,
		Name:          "web",
		Kind:          "app",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.UpdateServiceRuntimeStatus(svc.ID, "running", "cid-new", "ssc-watchapp-web-2", "img:2", 500, true); err != nil {
		t.Fatal(err)
	}

	var notified []string
	w := docker.NewWatcher(nil, store, func(_, _, id, st string) {
		notified = append(notified, id+":"+st)
	})

	// Draining old container emits die after swap -> MUST be ignored.
	w.HandleEvent(docker.EngineEvent{
		ContainerID: "cid-old",
		Action:      "die",
		Attributes:  map[string]string{docker.LabelServiceID: svc.ID},
		ObservedAt:  900,
	})
	got, _ := store.GetService(svc.ID)
	if got.Status != "running" || len(notified) != 0 {
		t.Fatalf("old container event corrupted active service: %+v notified=%v", got, notified)
	}

	// Active container OOM event -> MUST transition to failed and notify.
	w.HandleEvent(docker.EngineEvent{
		ContainerID: "cid-new",
		Action:      "oom",
		Attributes:  map[string]string{docker.LabelServiceID: svc.ID},
		ObservedAt:  1000,
	})
	got, _ = store.GetService(svc.ID)
	if got.Status != "failed" || len(notified) != 1 {
		t.Fatalf("expected active container OOM transition, got %+v notified=%v", got, notified)
	}
}
