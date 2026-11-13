package api

import (
	"context"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/doctor"
	"github.com/gachon-star-want/simple-server-control/internal/updater"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Router returns the complete HTTP handler wiring REST API, WebSockets, webhooks, and SPA UI.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		var active int64
		if s.pipeline != nil {
			active = s.pipeline.ActiveDeployments()
		}
		ver := ""
		if s.cfg != nil {
			ver = s.cfg.Version
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                 true,
			"version":            ver,
			"active_deployments": active,
		})
	})

	r.Route("/api", func(r chi.Router) {
		// Public auth & webhook endpoints
		r.Get("/auth/status", s.handleAuthStatus)
		r.Post("/auth/setup", s.handleSetup)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)
		r.Post("/webhooks/github", s.handleGitHubWebhook)

		// Authenticated operator API
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			r.Get("/auth/me", s.handleMe)

			// System & Diagnostics
			r.Get("/system/doctor", s.handleSystemDoctor)
			r.Get("/system/info", s.handleSystemInfo)
			r.Put("/system/panel-domain", s.handleSetPanelDomain)
			r.Get("/system/update", s.handleSystemUpdateStatus)
			r.Post("/system/update/auto", s.handleSystemUpdateToggle)
			r.Post("/system/update/apply", s.handleSystemUpdateApply)

			// GitHub App setup & discovery
			r.Get("/github/status", s.handleGitHubStatus)
			r.Post("/github/manifest", s.handleCreateGitHubManifest)
			r.Get("/github/callback", s.handleGitHubCallback)
			r.Get("/github/install", s.handleGitHubInstall)
			r.Get("/github/repos", s.handleListGitHubRepos)
			r.Get("/github/branches", s.handleListGitHubBranches)
			r.Delete("/github/app", s.handleDeleteGitHubApp)

			// Multi-Project hub & Canvas topology
			r.Get("/projects", s.handleListProjects)
			r.Post("/projects", s.handleCreateProject)
			r.Delete("/projects/{projectId}", s.handleDeleteProject)
			r.Get("/projects/{projectId}/canvas", s.handleGetProjectCanvas)
			r.Post("/projects/{projectId}/environments", s.handleCreateEnvironment)
			r.Put("/projects/{projectId}/canvas/positions", s.handleSaveCanvasPositions)
			r.Post("/projects/{projectId}/links", s.handleCreateLink)
			r.Delete("/projects/{projectId}/links/{linkId}", s.handleDeleteLink)
			r.Post("/projects/{projectId}/services", s.handleCreateService)

			// Shared Environment Secrets
			r.Get("/environments/{envId}/secrets", s.handleListSharedSecrets)
			r.Put("/environments/{envId}/secrets", s.handlePutSharedSecrets)
			r.Post("/environments/{envId}/secrets/import-env", s.handleImportSharedDotEnv)

			// Services, Deploys, Logs, Secrets, Domains
			r.Patch("/services/{serviceId}", s.handleUpdateService)
			r.Delete("/services/{serviceId}", s.handleDeleteService)
			r.Post("/services/{serviceId}/deploy", s.handleDeployService)
			r.Post("/services/{serviceId}/restart", s.handleRestartService)
			r.Post("/services/{serviceId}/stop", s.handleStopService)
			r.Get("/services/{serviceId}/deployments", s.handleListDeployments)
			r.Post("/services/{serviceId}/rollback", s.handleRollbackService)
			r.Get("/services/{serviceId}/stats", s.handleServiceStats)
			r.Get("/services/{serviceId}/logs", s.handleServiceLogsSnapshot)
			r.Get("/services/{serviceId}/secrets", s.handleListServiceSecrets)
			r.Put("/services/{serviceId}/secrets", s.handlePutServiceSecrets)
			r.Post("/services/{serviceId}/secrets/import-env", s.handleImportServiceDotEnv)
			r.Get("/services/{serviceId}/domains", s.handleListDomains)
			r.Post("/services/{serviceId}/domains", s.handleCreateDomain)

			r.Post("/domains/{domainId}/verify", s.handleVerifyDomain)
			r.Delete("/domains/{domainId}", s.handleDeleteDomain)

			// Real-time WebSocket endpoints
			r.Get("/ws/projects/{projectId}/canvas", s.handleWSCanvas)
			r.Get("/ws/deployments/{deploymentId}/logs", s.handleWSDeploymentLogs)
			r.Get("/ws/services/{serviceId}/logs", s.handleWSServiceLogs)
			r.Get("/ws/services/{serviceId}/terminal", s.handleWSTerminal)
		})

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "API_NOT_FOUND", "존재하지 않는 API 경로입니다.")
		})
	})

	if s.uiFS != nil {
		r.NotFound(s.handleSPAFallback)
	}
	return r
}

func (s *Server) handleSystemDoctor(w http.ResponseWriter, r *http.Request) {
	results := doctor.RunAll(r.Context(), s.engine, s.cfg.DataDir, s.cfg.CaddyAdminURL)
	writeJSON(w, http.StatusOK, map[string]any{"checks": results})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	panelDomain, _, _ := s.store.GetSetting("panel_domain")
	ver := ""
	if s.cfg != nil {
		ver = s.cfg.Version
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":       ver,
		"panel_domain":  panelDomain,
		"public_origin": s.detectPublicOrigin(r),
		"base_domain":   s.cfg.BaseDomain,
	})
}

func (s *Server) newUpdaterClient() *updater.Client {
	ver, repo := "", ""
	if s.cfg != nil {
		ver = s.cfg.Version
		repo = s.cfg.Repo
	}
	return updater.New(updater.Options{
		Repo:           repo,
		CurrentVersion: ver,
		ActiveDeployFn: func(context.Context) (int64, error) {
			if s.pipeline != nil {
				if n := s.pipeline.ActiveDeployments(); n > 0 {
					return n, nil
				}
			}
			if s.store != nil {
				n, err := s.store.CountActiveRecentDeployments(2 * time.Hour)
				return int64(n), err
			}
			return 0, nil
		},
	})
}

func (s *Server) handleSystemUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.newUpdaterClient().Status(r.Context()))
}

func (s *Server) handleSystemUpdateToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "요청 형식이 올바르지 않습니다.")
		return
	}
	if req.Enabled {
		binPath, _ := os.Executable()
		dataDir, repo := "/var/lib/ssc", updater.DefaultRepo
		if s.cfg != nil {
			dataDir = s.cfg.DataDir
			repo = s.cfg.Repo
		}
		if err := updater.EnableAutoUpdate(r.Context(), binPath, dataDir, repo); err != nil {
			writeError(w, http.StatusBadRequest, "AUTO_UPDATE_ERROR", err.Error())
			return
		}
	} else {
		if err := updater.DisableAutoUpdate(r.Context()); err != nil {
			writeError(w, http.StatusBadRequest, "AUTO_UPDATE_ERROR", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, s.newUpdaterClient().Status(r.Context()))
}

func (s *Server) handleSystemUpdateApply(w http.ResponseWriter, r *http.Request) {
	res, err := s.newUpdaterClient().ApplyUpgrade(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "UPGRADE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
	if res.Updated && res.RestartRequired && updater.RunningUnderSystemd() && s.onUpgradeRestart != nil {
		go func() {
			time.Sleep(400 * time.Millisecond)
			s.onUpgradeRestart()
		}()
	}
}

func (s *Server) handleSetPanelDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PanelDomain string `json:"panel_domain"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "요청 형식이 올바르지 않습니다.")
		return
	}
	dom := strings.ToLower(strings.TrimSpace(req.PanelDomain))
	if err := s.store.SetSetting("panel_domain", dom); err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	s.syncProxyBestEffort(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"panel_domain": dom})
}

func (s *Server) handleSPAFallback(w http.ResponseWriter, r *http.Request) {
	cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if cleanPath == "" || cleanPath == "." {
		cleanPath = "index.html"
	}

	if f, err := s.uiFS.Open(cleanPath); err == nil {
		stat, statErr := f.Stat()
		if statErr == nil && !stat.IsDir() {
			defer f.Close()
			if ct := mime.TypeByExtension(path.Ext(cleanPath)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			if strings.HasPrefix(cleanPath, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			_, _ = io.Copy(w, f)
			return
		}
		_ = f.Close()
	}

	// Missing static asset extension returns 404 rather than index HTML.
	if ext := path.Ext(cleanPath); ext != "" && cleanPath != "index.html" {
		http.NotFound(w, r)
		return
	}

	idx, err := s.uiFS.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer idx.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, idx)
}
