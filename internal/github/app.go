// Package github implements GitHub App Manifest 1-click setup, JWT installation verification,
// installation token minting, repository discovery, and webhook HMAC-SHA256 validation.
package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/golang-jwt/jwt/v5"
)

const stateSettingPrefix = "gh_state:"

// RepoSummary describes an accessible repository in a GitHub App installation.
type RepoSummary struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	HTMLURL       string `json:"html_url"`
	Description   string `json:"description"`
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// Client interacts with the GitHub REST API on behalf of stored GitHub Apps.
type Client struct {
	store      *db.Store
	apiBaseURL string
	httpClient *http.Client

	tokMu  sync.Mutex
	tokens map[string]cachedToken
}

// NewClient constructs a GitHub API client targeting apiBaseURL (defaults to https://api.github.com).
func NewClient(store *db.Store, apiBaseURL string) *Client {
	if apiBaseURL == "" {
		apiBaseURL = "https://api.github.com"
	}
	return &Client{
		store:      store,
		apiBaseURL: strings.TrimRight(apiBaseURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
		tokens:     make(map[string]cachedToken),
	}
}

// CreateState generates and persists a single-use CSRF state token in SQLite.
func (c *Client) CreateState(purpose string) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	state := hex.EncodeToString(buf)
	exp := time.Now().Add(20 * time.Minute).UTC().Format(time.RFC3339)
	if err := c.store.SetSetting(stateSettingPrefix+state, purpose+"|"+exp); err != nil {
		return "", err
	}
	return state, nil
}

// ConsumeState validates and atomically invalidates a single-use CSRF state token.
func (c *Client) ConsumeState(state, expectedPurpose string) error {
	state = strings.TrimSpace(state)
	if state == "" {
		return fmt.Errorf("missing github state parameter")
	}
	raw, ok, err := c.store.GetSetting(stateSettingPrefix + state)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("invalid or expired github state")
	}
	_ = c.store.DeleteSetting(stateSettingPrefix + state)

	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		return fmt.Errorf("malformed stored state")
	}
	if expectedPurpose != "" && !hmac.Equal([]byte(parts[0]), []byte(expectedPurpose)) {
		return fmt.Errorf("mismatched github state purpose")
	}
	exp, err := time.Parse(time.RFC3339, parts[1])
	if err != nil || time.Now().UTC().After(exp) {
		return fmt.Errorf("github setup state expired")
	}
	return nil
}

// BuildManifest generates the GitHub App Manifest JSON payload for 1-click browser creation.
func BuildManifest(appName, publicOrigin, state string) (string, error) {
	origin := strings.TrimRight(strings.TrimSpace(publicOrigin), "/")
	if origin == "" {
		return "", fmt.Errorf("public origin is required for GitHub App manifest")
	}
	if appName == "" {
		appName = "ssc-deploy-" + state[:6]
	}
	manifest := map[string]any{
		"name":        appName,
		"url":         origin,
		"redirect_url": fmt.Sprintf("%s/api/github/callback?state=%s", origin, state),
		"setup_url":    fmt.Sprintf("%s/api/github/install?state=%s", origin, state),
		"setup_on_update": true,
		"public":      false,
		"hook_attributes": map[string]any{
			"url":    origin + "/api/webhooks/github",
			"active": true,
		},
		"default_permissions": map[string]string{
			"contents": "read",
			"metadata": "read",
			"statuses": "write",
		},
		"default_events": []string{"push"},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ExchangeManifestCode converts a temporary GitHub App Manifest code into encrypted credentials.
func (c *Client) ExchangeManifestCode(ctx context.Context, code string) (*db.GitHubApp, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("missing manifest code")
	}
	url := fmt.Sprintf("%s/app-manifests/%s/conversions", c.apiBaseURL, code)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "simple-server-control")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange github manifest code: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github manifest conversion failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		HTMLURL       string `json:"html_url"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode manifest conversion response: %w", err)
	}

	return c.store.SaveGitHubApp(db.GitHubApp{
		AppID:         payload.ID,
		Slug:          payload.Slug,
		Name:          payload.Name,
		HTMLURL:       payload.HTMLURL,
		ClientID:      payload.ClientID,
		ClientSecret:  payload.ClientSecret,
		WebhookSecret: payload.WebhookSecret,
		PrivateKeyPEM: payload.PEM,
	})
}

// VerifyAndSaveInstallation cross-checks installation_id against GitHub API using the App's JWT
// before trusting or persisting it in SQLite.
func (c *Client) VerifyAndSaveInstallation(ctx context.Context, appRecordID string, installationID int64) (*db.GitHubApp, error) {
	if installationID <= 0 {
		return nil, fmt.Errorf("invalid installation_id")
	}
	var app *db.GitHubApp
	var err error
	if appRecordID != "" {
		app, err = c.store.GetGitHubApp(appRecordID)
	} else {
		app, err = c.store.GetPrimaryGitHubApp()
	}
	if err != nil {
		return nil, err
	}

	appJWT, err := SignAppJWT(app.AppID, app.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("sign app jwt: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d", c.apiBaseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "simple-server-control")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("verify github installation: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("installation verification failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var inst struct {
		ID      int64 `json:"id"`
		AppID   int64 `json:"app_id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &inst); err != nil {
		return nil, fmt.Errorf("decode installation verification: %w", err)
	}
	if inst.ID != installationID || (inst.AppID != 0 && inst.AppID != app.AppID) {
		return nil, fmt.Errorf("github installation ownership mismatch")
	}

	c.tokMu.Lock()
	delete(c.tokens, app.ID)
	c.tokMu.Unlock()

	return c.store.UpdateGitHubAppInstallation(app.ID, inst.ID, inst.Account.Login, inst.Account.Type)
}

// InstallationToken obtains (and caches) a short-lived GitHub App installation access token.
func (c *Client) InstallationToken(ctx context.Context, app *db.GitHubApp) (string, error) {
	if app == nil || app.InstallationID <= 0 {
		return "", fmt.Errorf("github app is not installed yet")
	}

	c.tokMu.Lock()
	if cached, ok := c.tokens[app.ID]; ok && time.Now().Add(2*time.Minute).Before(cached.expiresAt) {
		tok := cached.token
		c.tokMu.Unlock()
		return tok, nil
	}
	c.tokMu.Unlock()

	appJWT, err := SignAppJWT(app.AppID, app.PrivateKeyPEM)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiBaseURL, app.InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "simple-server-control")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request installation token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("mint installation token failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}

	exp := out.ExpiresAt
	if exp.IsZero() {
		exp = time.Now().Add(50 * time.Minute)
	}
	c.tokMu.Lock()
	c.tokens[app.ID] = cachedToken{token: out.Token, expiresAt: exp}
	c.tokMu.Unlock()
	return out.Token, nil
}

// ListRepositories returns repositories accessible by the verified GitHub App installation.
func (c *Client) ListRepositories(ctx context.Context, app *db.GitHubApp) ([]RepoSummary, error) {
	tok, err := c.InstallationToken(ctx, app)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/installation/repositories?per_page=100", c.apiBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "simple-server-control")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list repositories failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		Repositories []RepoSummary `json:"repositories"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Repositories, nil
}

// ListBranches lists branch names of an installation-accessible repository.
func (c *Client) ListBranches(ctx context.Context, app *db.GitHubApp, repoFullName string) ([]string, error) {
	tok, err := c.InstallationToken(ctx, app)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/repos/%s/branches?per_page=100", c.apiBaseURL, repoFullName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "simple-server-control")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list branches failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var items []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	return names, nil
}

// ReportCommitStatus updates commit deployment status on GitHub (pending, success, failure).
func (c *Client) ReportCommitStatus(ctx context.Context, app *db.GitHubApp, repoFullName, sha, state, targetURL, description string) error {
	if app == nil || app.InstallationID <= 0 || repoFullName == "" || sha == "" {
		return nil
	}
	tok, err := c.InstallationToken(ctx, app)
	if err != nil {
		return err
	}
	if len(description) > 135 {
		description = description[:135]
	}
	payload := map[string]string{
		"state":       state,
		"context":     "simple-server-control",
		"description": description,
	}
	if targetURL != "" {
		payload["target_url"] = targetURL
	}
	raw, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/repos/%s/statuses/%s", c.apiBaseURL, repoFullName, sha)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "simple-server-control")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("github status post returned %d", resp.StatusCode)
	}
	return nil
}

// SignAppJWT signs a short-lived RS256 GitHub App JWT (10m max per GitHub specification).
func SignAppJWT(appID int64, pemKey string) (string, error) {
	priv, err := parseRSAPrivateKey(pemKey)
	if err != nil {
		return "", err
	}
	now := time.Now().Add(-30 * time.Second)
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(appID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(priv)
}

func parseRSAPrivateKey(rawPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(rawPEM))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA private key: %w", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}
	return rsaKey, nil
}
