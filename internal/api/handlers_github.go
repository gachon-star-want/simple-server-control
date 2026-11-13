package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/gachon-star-want/simple-server-control/internal/github"
)

func (s *Server) detectPublicOrigin(r *http.Request) string {
	if s.cfg != nil && strings.TrimSpace(s.cfg.PublicOrigin) != "" {
		return strings.TrimRight(strings.TrimSpace(s.cfg.PublicOrigin), "/")
	}
	if panelDom, ok, _ := s.store.GetSetting("panel_domain"); ok && panelDom != "" {
		return "https://" + strings.TrimSpace(panelDom)
	}
	proto := "http"
	if isHTTPSRequest(r) {
		proto = "https"
	}
	return proto + "://" + r.Host
}

func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.ListGitHubApps()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apps":            apps,
		"detected_origin": s.detectPublicOrigin(r),
	})
}

func (s *Server) handleCreateGitHubManifest(w http.ResponseWriter, r *http.Request) {
	if s.ghClient == nil {
		writeError(w, http.StatusServiceUnavailable, "GITHUB_DISABLED", "GitHub 클라이언트가 비활성화되어 있습니다.")
		return
	}
	var req struct {
		AppName      string `json:"app_name"`
		PublicOrigin string `json:"public_origin"`
		Organization string `json:"organization"`
	}
	_ = decodeJSON(r, &req)

	origin := strings.TrimRight(strings.TrimSpace(req.PublicOrigin), "/")
	if origin == "" {
		origin = s.detectPublicOrigin(r)
	}

	state, err := s.ghClient.CreateState("manifest")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STATE_ERROR", err.Error())
		return
	}
	manifest, err := github.BuildManifest(req.AppName, origin, state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "MANIFEST_ERROR", err.Error())
		return
	}

	actionURL := "https://github.com/settings/apps/new?state=" + state
	if org := strings.TrimSpace(req.Organization); org != "" {
		actionURL = fmt.Sprintf("https://github.com/organizations/%s/settings/apps/new?state=%s", org, state)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action_url": actionURL,
		"manifest":   manifest,
		"state":      state,
	})
}

func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if err := s.ghClient.ConsumeState(state, "manifest"); err != nil {
		http.Redirect(w, r, "/settings?github_error="+urlQueryEscape(err.Error()), http.StatusFound)
		return
	}
	app, err := s.ghClient.ExchangeManifestCode(r.Context(), code)
	if err != nil {
		http.Redirect(w, r, "/settings?github_error="+urlQueryEscape(err.Error()), http.StatusFound)
		return
	}

	installState, err := s.ghClient.CreateState("install")
	if err != nil || app.Slug == "" {
		http.Redirect(w, r, "/settings?github=created", http.StatusFound)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%s", app.Slug, installState), http.StatusFound)
}

func (s *Server) handleGitHubInstall(w http.ResponseWriter, r *http.Request) {
	if states := r.URL.Query()["state"]; len(states) > 0 {
		var lastErr error
		matched := false
		for _, candidate := range states {
			if candidate == "" {
				continue
			}
			if err := s.ghClient.ConsumeState(candidate, "install"); err == nil {
				matched = true
				break
			} else {
				lastErr = err
			}
		}
		if lastErr != nil && !matched {
			http.Redirect(w, r, "/settings?github_error="+urlQueryEscape(lastErr.Error()), http.StatusFound)
			return
		}
	}

	instIDStr := r.URL.Query().Get("installation_id")
	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil || instID <= 0 {
		http.Redirect(w, r, "/settings?github_error="+urlQueryEscape("유효한 installation_id가 없습니다"), http.StatusFound)
		return
	}

	if _, err := s.ghClient.VerifyAndSaveInstallation(r.Context(), "", instID); err != nil {
		http.Redirect(w, r, "/settings?github_error="+urlQueryEscape(err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/settings?github=connected", http.StatusFound)
}

func (s *Server) handleListGitHubRepos(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetPrimaryGitHubApp()
	if err != nil {
		writeError(w, http.StatusBadRequest, "GITHUB_NOT_CONNECTED", "먼저 설정 화면에서 GitHub App을 연결해 주세요.")
		return
	}
	repos, err := s.ghClient.ListRepositories(r.Context(), app)
	if err != nil {
		writeError(w, http.StatusBadGateway, "GITHUB_API_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repos, "app_id": app.ID})
}

func (s *Server) handleListGitHubBranches(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repo == "" {
		writeError(w, http.StatusBadRequest, "MISSING_REPO", "repo 쿼리 파라미터가 필요합니다.")
		return
	}
	app, err := s.store.GetPrimaryGitHubApp()
	if err != nil {
		writeError(w, http.StatusBadRequest, "GITHUB_NOT_CONNECTED", "GitHub App이 연결되어 있지 않습니다.")
		return
	}
	branches, err := s.ghClient.ListBranches(r.Context(), app, repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, "GITHUB_API_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": branches})
}

func (s *Server) handleDeleteGitHubApp(w http.ResponseWriter, r *http.Request) {
	appID := strings.TrimSpace(r.URL.Query().Get("id"))
	if appID == "" {
		app, err := s.store.GetPrimaryGitHubApp()
		if err != nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "연결된 GitHub App이 없습니다.")
			return
		}
		appID = app.ID
	}
	if err := s.store.DeleteGitHubApp(appID); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "GitHub App을 찾을 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "READ_ERROR", "Webhook 요청을 읽을 수 없습니다.")
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	apps, err := s.store.ListGitHubApps()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	var matchedApp *db.GitHubApp
	for i := range apps {
		if github.VerifySignature(apps[i].WebhookSecret, payload, sig) {
			matchedApp = &apps[i]
			break
		}
	}
	if matchedApp == nil {
		writeError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "GitHub Webhook HMAC 서명이 유효하지 않습니다.")
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "ping" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": "ping"})
		return
	}
	if eventType != "push" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored_event": eventType})
		return
	}

	pushEvt, err := github.ParsePushEvent(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAYLOAD", err.Error())
		return
	}
	if pushEvt.Deleted {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "branch_deleted"})
		return
	}

	services, err := s.store.FindAutoDeployServices(pushEvt.RepoFullName, pushEvt.Branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	triggered := make([]string, 0, len(services))
	for _, svc := range services {
		if s.pipeline != nil {
			if dep, err := s.pipeline.StartAppDeployment(svc.ID, "webhook", pushEvt.CommitSHA, pushEvt.CommitMessage); err == nil {
				triggered = append(triggered, dep.ID)
			}
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":                 true,
		"matched_services":   len(services),
		"deployment_ids":     triggered,
		"repo":               pushEvt.RepoFullName,
		"branch":             pushEvt.Branch,
	})
}

func urlQueryEscape(s string) string {
	repl := strings.NewReplacer(" ", "%20", "&", "%26", "#", "%23", "?", "%3F")
	return repl.Replace(s)
}
