// Package api implements REST endpoints, WebSocket streaming, GitHub webhooks, and SPA serving.
package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"

	"github.com/gachon-star-want/simple-server-control/internal/config"
	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/deploy"
	"github.com/gachon-star-want/simple-server-control/internal/docker"
	"github.com/gachon-star-want/simple-server-control/internal/github"
	"github.com/gachon-star-want/simple-server-control/internal/proxy"
)

// CanvasEvent represents a real-time status or topology change pushed to connected Canvas views.
type CanvasEvent struct {
	Type          string `json:"type"`
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
	ServiceID     string `json:"service_id,omitempty"`
	Status        string `json:"status,omitempty"`
}

// CanvasHub fans out live service status & graph updates to subscribed WebSocket clients per project.
type CanvasHub struct {
	mu      sync.RWMutex
	clients map[string]map[chan CanvasEvent]struct{}
}

// NewCanvasHub constructs a thread-safe project-scoped WebSocket broadcast hub.
func NewCanvasHub() *CanvasHub {
	return &CanvasHub{clients: make(map[string]map[chan CanvasEvent]struct{})}
}

// Subscribe registers a listener channel for projectID and returns an unsubscribe cleanup callback.
func (h *CanvasHub) Subscribe(projectID string) (<-chan CanvasEvent, func()) {
	ch := make(chan CanvasEvent, 32)
	h.mu.Lock()
	if h.clients[projectID] == nil {
		h.clients[projectID] = make(map[chan CanvasEvent]struct{})
	}
	h.clients[projectID][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if set := h.clients[projectID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.clients, projectID)
			}
		}
		h.mu.Unlock()
	}
}

// Broadcast publishes a canvas update to all open viewers of evt.ProjectID.
func (h *CanvasHub) Broadcast(evt CanvasEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients[evt.ProjectID] {
		select {
		case ch <- evt:
		default:
		}
	}
}

// Server bundles backend subsystems exposed over HTTP/WebSocket.
type Server struct {
	cfg      *config.Config
	store    *db.Store
	engine   docker.Engine
	router   proxy.Router
	pipeline *deploy.Pipeline
	ghClient *github.Client
	hub      *CanvasHub
	uiFS     fs.FS
}

// NewServer constructs an API server instance.
func NewServer(
	cfg *config.Config,
	store *db.Store,
	engine docker.Engine,
	router proxy.Router,
	pipeline *deploy.Pipeline,
	ghClient *github.Client,
	hub *CanvasHub,
	uiFS fs.FS,
) *Server {
	if hub == nil {
		hub = NewCanvasHub()
	}
	return &Server{
		cfg:      cfg,
		store:    store,
		engine:   engine,
		router:   router,
		pipeline: pipeline,
		ghClient: ghClient,
		hub:      hub,
		uiFS:     uiFS,
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": msg,
		"code":  code,
	})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	return dec.Decode(dst)
}
