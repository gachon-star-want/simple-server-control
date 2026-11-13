package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	proj, err := s.store.GetProject(projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "프로젝트를 찾을 수 없습니다.")
		return
	}

	var req struct {
		db.CreateServiceInput
		AutoDomain *bool `json:"auto_domain,omitempty"`
		AutoDeployNow bool `json:"deploy_now"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "잘못된 요청 형식입니다.")
		return
	}

	req.CreateServiceInput.ProjectID = projectID
	if req.EnvironmentID == "" {
		envs, envErr := s.store.ListEnvironments(projectID)
		if envErr != nil || len(envs) == 0 {
			writeError(w, http.StatusBadRequest, "ENV_MISSING", "프로젝트 환경을 찾을 수 없습니다.")
			return
		}
		req.EnvironmentID = envs[0].ID
	}

	svc, err := s.store.CreateService(req.CreateServiceInput)
	if err != nil {
		writeError(w, http.StatusBadRequest, "SERVICE_CREATE_FAILED", err.Error())
		return
	}

	var createdDomain *db.Domain
	if svc.Kind == "app" && (req.AutoDomain == nil || *req.AutoDomain) && s.router != nil {
		autoHost := s.router.AutoDomain(svc.Slug, proj.Slug)
		if dom, domErr := s.store.CreateDomain(svc.ID, autoHost, true, true, true); domErr == nil {
			createdDomain = dom
			s.syncProxyBestEffort(r.Context())
		}
	}

	var provisionWarning string
	if svc.Kind == "database" && s.pipeline != nil && s.engine != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		if err := s.pipeline.ProvisionDatabase(ctx, svc); err != nil {
			provisionWarning = err.Error()
		}
		cancel()
	}

	var deployment *db.Deployment
	if svc.Kind == "app" && req.AutoDeployNow && svc.RepoFullName != "" && s.pipeline != nil {
		deployment, _ = s.pipeline.StartAppDeployment(svc.ID, "manual", "", "")
	}

	fresh, _ := s.store.GetService(svc.ID)
	if fresh != nil {
		svc = fresh
	}
	s.hub.Broadcast(CanvasEvent{Type: "canvas_refresh", ProjectID: projectID, EnvironmentID: svc.EnvironmentID, ServiceID: svc.ID})
	writeJSON(w, http.StatusCreated, map[string]any{
		"service":           svc,
		"domain":            createdDomain,
		"deployment":        deployment,
		"provision_warning": provisionWarning,
	})
}

func (s *Server) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	var req db.UpdateServiceInput
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "잘못된 요청 형식입니다.")
		return
	}
	svc, err := s.store.UpdateService(serviceID, req)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "UPDATE_FAILED", err.Error())
		return
	}
	if req.Port != nil && svc.Kind == "app" {
		s.syncProxyBestEffort(r.Context())
	}
	s.hub.Broadcast(CanvasEvent{Type: "canvas_refresh", ProjectID: svc.ProjectID, EnvironmentID: svc.EnvironmentID, ServiceID: svc.ID})
	writeJSON(w, http.StatusOK, map[string]any{"service": svc})
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if svc.ActiveContainerID != "" && s.engine != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		_ = s.engine.StopContainer(ctx, svc.ActiveContainerID, 5*time.Second)
		_ = s.engine.RemoveContainer(ctx, svc.ActiveContainerID)
		cancel()
	}

	if err := s.store.DeleteService(serviceID); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}
	s.syncProxyBestEffort(r.Context())
	s.hub.Broadcast(CanvasEvent{Type: "canvas_refresh", ProjectID: svc.ProjectID, EnvironmentID: svc.EnvironmentID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeployService(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	if s.pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "NO_PIPELINE", "배포 파이프라인이 초기화되지 않았습니다.")
		return
	}
	dep, err := s.pipeline.StartAppDeployment(serviceID, "manual", "", "")
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "DEPLOY_START_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"deployment": dep})
}

func (s *Server) handleRestartService(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	if s.pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "NO_PIPELINE", "파이프라인이 준비되지 않았습니다.")
		return
	}
	if err := s.pipeline.RestartService(r.Context(), serviceID); err != nil {
		writeError(w, http.StatusBadRequest, "RESTART_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleStopService(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	if s.pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "NO_PIPELINE", "파이프라인이 준비되지 않았습니다.")
		return
	}
	if err := s.pipeline.StopService(r.Context(), serviceID); err != nil {
		writeError(w, http.StatusBadRequest, "STOP_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	deps, err := s.store.ListDeploymentsByService(serviceID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": deps})
}

func (s *Server) handleRollbackService(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	var req struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := decodeJSON(r, &req); err != nil || req.DeploymentID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "복원할 배포 ID가 필요합니다.")
		return
	}
	if err := s.pipeline.Rollback(r.Context(), serviceID, req.DeploymentID); err != nil {
		writeError(w, http.StatusBadRequest, "ROLLBACK_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleServiceStats(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	if svc.ActiveContainerID == "" || s.engine == nil {
		writeJSON(w, http.StatusOK, map[string]any{"stats": nil})
		return
	}
	stats, err := s.engine.OneShotStats(r.Context(), svc.ActiveContainerID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"stats": nil, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats})
}

func (s *Server) handleServiceLogsSnapshot(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	if svc.ActiveContainerID == "" || s.engine == nil {
		writeJSON(w, http.StatusOK, map[string]any{"logs": ""})
		return
	}
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "200"
	}
	rc, err := s.engine.StreamLogs(r.Context(), svc.ActiveContainerID, tail, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LOGS_ERROR", err.Error())
		return
	}
	defer rc.Close()
	raw, _ := io.ReadAll(io.LimitReader(rc, 512*1024))
	writeJSON(w, http.StatusOK, map[string]any{"logs": string(raw)})
}

func (s *Server) handleListServiceSecrets(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	s.respondListSecrets(w, r, svc.EnvironmentID, svc.ID)
}

func (s *Server) handlePutServiceSecrets(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	s.handleReplaceSecrets(w, r, svc.ProjectID, svc.EnvironmentID, svc.ID)
}

func (s *Server) handleImportServiceDotEnv(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	s.handleImportDotEnv(w, r, svc.ProjectID, svc.EnvironmentID, svc.ID)
}

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	domains, err := s.store.ListDomainsByService(serviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": domains})
}

func (s *Server) handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "서비스를 찾을 수 없습니다.")
		return
	}
	var req struct {
		Hostname     string `json:"hostname"`
		HTTPSEnabled *bool  `json:"https_enabled,omitempty"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "잘못된 요청 형식입니다.")
		return
	}

	hostname := strings.ToLower(strings.TrimSpace(req.Hostname))
	httpsEnabled := true
	if req.HTTPSEnabled != nil {
		httpsEnabled = *req.HTTPSEnabled
	}

	var dnsVerified bool
	var dnsMsg string
	var serverIP string
	if s.router != nil && hostname != "" {
		dnsVerified, _, serverIP, dnsMsg = s.router.VerifyDNS(r.Context(), hostname)
	}

	dom, err := s.store.CreateDomain(svc.ID, hostname, false, httpsEnabled, dnsVerified)
	if err != nil {
		writeError(w, http.StatusBadRequest, "DOMAIN_ADD_FAILED", err.Error())
		return
	}
	s.syncProxyBestEffort(r.Context())
	s.hub.Broadcast(CanvasEvent{Type: "canvas_refresh", ProjectID: svc.ProjectID, EnvironmentID: svc.EnvironmentID, ServiceID: svc.ID})
	writeJSON(w, http.StatusCreated, map[string]any{
		"domain":      dom,
		"server_ip":   serverIP,
		"dns_message": dnsMsg,
	})
}

func (s *Server) handleVerifyDomain(w http.ResponseWriter, r *http.Request) {
	domainID := chi.URLParam(r, "domainId")
	dom, err := s.store.GetDomain(domainID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "도메인을 찾을 수 없습니다.")
		return
	}
	if s.router == nil {
		writeJSON(w, http.StatusOK, map[string]any{"domain": dom, "verified": dom.DNSVerified})
		return
	}

	verified, ips, serverIP, msg := s.router.VerifyDNS(r.Context(), dom.Hostname)
	updated, err := s.store.UpdateDomainStatus(dom.ID, verified, dom.HTTPSEnabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if verified {
		s.syncProxyBestEffort(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domain":       updated,
		"verified":     verified,
		"resolved_ips": ips,
		"server_ip":    serverIP,
		"message":      msg,
	})
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	domainID := chi.URLParam(r, "domainId")
	if err := s.store.DeleteDomain(domainID); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "도메인을 찾을 수 없습니다.")
		return
	}
	s.syncProxyBestEffort(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) syncProxyBestEffort(ctx context.Context) {
	if s.router == nil {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = s.router.Sync(syncCtx)
}
