package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/envref"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListProjectOverviews()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if items == nil {
		items = []db.ProjectOverview{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": items})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "요청 형식이 올바르지 않습니다.")
		return
	}

	proj, env, err := s.store.CreateProject(req.Name, req.Description)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PROJECT_CREATE_FAILED", err.Error())
		return
	}

	if s.engine != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		_ = s.engine.EnsureNetwork(ctx, proj.NetworkName)
		cancel()
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"project":     proj,
		"environment": env,
	})
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	proj, err := s.store.GetProject(projectID)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "프로젝트를 찾을 수 없습니다.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if s.engine != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		containers, _ := s.engine.ListManagedContainers(ctx, proj.ID)
		for _, c := range containers {
			_ = s.engine.StopContainer(ctx, c.ID, 5*time.Second)
			_ = s.engine.RemoveContainer(ctx, c.ID)
		}
		_ = s.engine.RemoveNetwork(ctx, proj.NetworkName)
		cancel()
	}

	if err := s.store.DeleteProject(projectID); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}
	s.syncProxyBestEffort(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetProjectCanvas(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	envID := r.URL.Query().Get("env_id")

	canvas, err := s.store.GetProjectCanvas(projectID, envID)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "프로젝트 또는 환경을 찾을 수 없습니다.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, canvas)
}

func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "잘못된 요청 형식입니다.")
		return
	}
	env, err := s.store.CreateEnvironment(projectID, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ENV_CREATE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"environment": env})
}

func (s *Server) handleSaveCanvasPositions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EnvironmentID string               `json:"environment_id"`
		Positions     []db.ServicePosition `json:"positions"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "잘못된 요청 형식입니다.")
		return
	}
	if err := s.store.UpdateServicePositions(req.EnvironmentID, req.Positions); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_POSITIONS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	var req struct {
		EnvironmentID   string `json:"environment_id"`
		SourceServiceID string `json:"source_service_id"`
		TargetServiceID string `json:"target_service_id"`
		InjectedEnvKey  string `json:"injected_env_key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "잘못된 요청 형식입니다.")
		return
	}
	if req.InjectedEnvKey == "" {
		req.InjectedEnvKey = "DATABASE_URL"
	}

	link, err := s.store.CreateServiceLink(projectID, req.EnvironmentID, req.SourceServiceID, req.TargetServiceID, req.InjectedEnvKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "LINK_FAILED", err.Error())
		return
	}
	s.hub.Broadcast(CanvasEvent{Type: "canvas_refresh", ProjectID: projectID, EnvironmentID: req.EnvironmentID})
	writeJSON(w, http.StatusCreated, map[string]any{"link": link})
}

func (s *Server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	linkID := chi.URLParam(r, "linkId")
	if err := s.store.DeleteServiceLink(linkID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "연결선을 찾을 수 없습니다.")
			return
		}
		writeError(w, http.StatusInternalServerError, "DELETE_LINK_FAILED", err.Error())
		return
	}
	s.hub.Broadcast(CanvasEvent{Type: "canvas_refresh", ProjectID: projectID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListSharedSecrets(w http.ResponseWriter, r *http.Request) {
	envID := chi.URLParam(r, "envId")
	s.respondListSecrets(w, r, envID, "")
}

func (s *Server) handlePutSharedSecrets(w http.ResponseWriter, r *http.Request) {
	envID := chi.URLParam(r, "envId")
	env, err := s.store.GetEnvironment(envID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "환경을 찾을 수 없습니다.")
		return
	}
	s.handleReplaceSecrets(w, r, env.ProjectID, env.ID, "")
}

func (s *Server) handleImportSharedDotEnv(w http.ResponseWriter, r *http.Request) {
	envID := chi.URLParam(r, "envId")
	env, err := s.store.GetEnvironment(envID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "환경을 찾을 수 없습니다.")
		return
	}
	s.handleImportDotEnv(w, r, env.ProjectID, env.ID, "")
}

func (s *Server) respondListSecrets(w http.ResponseWriter, r *http.Request, envID, serviceID string) {
	reveal := r.URL.Query().Get("reveal") == "true"
	secrets, err := s.store.ListSecrets(envID, serviceID, reveal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SECRETS_LIST_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": secrets})
}

func (s *Server) handleReplaceSecrets(w http.ResponseWriter, r *http.Request, projectID, envID, serviceID string) {
	var req struct {
		Secrets []db.SecretInput `json:"secrets"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "시크릿 목록 형식이 올바르지 않습니다.")
		return
	}
	if err := s.store.ReplaceSecretsBulk(envID, serviceID, req.Secrets); err != nil {
		writeError(w, http.StatusBadRequest, "SECRETS_SAVE_FAILED", err.Error())
		return
	}
	s.hub.Broadcast(CanvasEvent{Type: "canvas_refresh", ProjectID: projectID, EnvironmentID: envID, ServiceID: serviceID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleImportDotEnv(w http.ResponseWriter, r *http.Request, projectID, envID, serviceID string) {
	var req struct {
		Raw      string `json:"raw"`
		IsSealed bool   `json:"is_sealed"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "잘못된 요청 형식입니다.")
		return
	}
	parsed, err := envref.ParseDotEnv(req.Raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "DOTENV_PARSE_ERROR", err.Error())
		return
	}
	for k, v := range parsed {
		if err := s.store.UpsertSecret(envID, serviceID, db.SecretInput{
			Key:      k,
			Value:    v,
			IsSealed: req.IsSealed,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "SECRET_UPSERT_FAILED", err.Error())
			return
		}
	}
	s.hub.Broadcast(CanvasEvent{Type: "canvas_refresh", ProjectID: projectID, EnvironmentID: envID, ServiceID: serviceID})
	writeJSON(w, http.StatusOK, map[string]any{"imported": len(parsed)})
}
