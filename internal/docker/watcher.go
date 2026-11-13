package docker

import (
	"context"
	"log/slog"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/db"
)

// Watcher streams Docker Engine events for managed containers and updates SQLite service states
// safely, guarding against stale or replaced-container events.
type Watcher struct {
	engine   Engine
	store    *db.Store
	onChange func(projectID, envID, serviceID, status string)
}

// NewWatcher creates a background container event reconciler.
func NewWatcher(engine Engine, store *db.Store, onChange func(projectID, envID, serviceID, status string)) *Watcher {
	return &Watcher{
		engine:   engine,
		store:    store,
		onChange: onChange,
	}
}

// Run blocks watching Docker events with automatic backoff reconnect until ctx is canceled.
func (w *Watcher) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		err := w.engine.WatchEvents(ctx, w.HandleEvent)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("docker event stream interrupted, retrying", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// HandleEvent reconciles one Docker container lifecycle event into SQLite.
func (w *Watcher) HandleEvent(evt EngineEvent) {
	serviceID := evt.Attributes[LabelServiceID]
	if serviceID == "" {
		return
	}

	nextStatus, ok := mapActionToStatus(evt.Action)
	if !ok {
		return
	}

	svc, err := w.store.GetService(serviceID)
	if err != nil {
		return
	}

	// Only accept lifecycle events for the currently active container so draining/old
	// or candidate containers during zero-downtime swaps never corrupt node status.
	if svc.ActiveContainerID == "" || svc.ActiveContainerID != evt.ContainerID {
		return
	}

	updated, err := w.store.UpdateServiceRuntimeStatus(serviceID, nextStatus, evt.ContainerID, "", "", evt.ObservedAt, false)
	if err != nil {
		slog.Warn("failed to update service status from docker event", "service_id", serviceID, "error", err)
		return
	}
	if updated && w.onChange != nil {
		w.onChange(svc.ProjectID, svc.EnvironmentID, svc.ID, nextStatus)
	}
}

func mapActionToStatus(action string) (string, bool) {
	switch action {
	case "start", "unpause", "health_status: healthy":
		return "running", true
	case "oom", "kill":
		return "failed", true
	case "die", "stop":
		return "stopped", true
	default:
		return "", false
	}
}
