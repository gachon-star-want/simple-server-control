package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SaveGitHubApp persists a GitHub App manifest exchange result with encrypted secrets and private key.
func (s *Store) SaveGitHubApp(app GitHubApp) (*GitHubApp, error) {
	if app.ID == "" {
		app.ID = uuid.NewString()
	}
	clientSecretEnc, err := s.vault.Encrypt(app.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("encrypt github client secret: %w", err)
	}
	webhookSecretEnc, err := s.vault.Encrypt(app.WebhookSecret)
	if err != nil {
		return nil, fmt.Errorf("encrypt github webhook secret: %w", err)
	}
	privateKeyEnc, err := s.vault.Encrypt(app.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("encrypt github private key: %w", err)
	}

	now := time.Now().UTC()
	app.CreatedAt = now
	app.UpdatedAt = now
	app.Connected = app.InstallationID > 0

	_, err = s.db.Exec(`
		INSERT INTO github_apps (
			id, app_id, slug, name, html_url, client_id,
			client_secret_enc, webhook_secret_enc, private_key_enc,
			installation_id, account_login, account_type, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			app_id = excluded.app_id,
			slug = excluded.slug,
			name = excluded.name,
			html_url = excluded.html_url,
			client_id = excluded.client_id,
			client_secret_enc = excluded.client_secret_enc,
			webhook_secret_enc = excluded.webhook_secret_enc,
			private_key_enc = excluded.private_key_enc,
			installation_id = excluded.installation_id,
			account_login = excluded.account_login,
			account_type = excluded.account_type,
			updated_at = CURRENT_TIMESTAMP
	`,
		app.ID, app.AppID, app.Slug, app.Name, app.HTMLURL, app.ClientID,
		clientSecretEnc, webhookSecretEnc, privateKeyEnc,
		app.InstallationID, app.AccountLogin, app.AccountType, app.CreatedAt, app.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("save github app: %w", err)
	}
	return s.GetGitHubApp(app.ID)
}

// UpdateGitHubAppInstallation binds a verified GitHub App installation ID and owner account.
func (s *Store) UpdateGitHubAppInstallation(id string, installationID int64, accountLogin, accountType string) (*GitHubApp, error) {
	res, err := s.db.Exec(`
		UPDATE github_apps
		SET installation_id = ?, account_login = ?, account_type = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, installationID, accountLogin, accountType, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetGitHubApp(id)
}

// GetGitHubApp retrieves a GitHub App by record ID, decrypting its credentials for backend use.
func (s *Store) GetGitHubApp(id string) (*GitHubApp, error) {
	row := s.db.QueryRow(`SELECT `+githubAppCols+` FROM github_apps WHERE id = ?`, id)
	return s.scanGitHubApp(row)
}

// GetGitHubAppByAppID finds a registered GitHub App by GitHub's numeric app ID.
func (s *Store) GetGitHubAppByAppID(appID int64) (*GitHubApp, error) {
	row := s.db.QueryRow(`SELECT `+githubAppCols+` FROM github_apps WHERE app_id = ? LIMIT 1`, appID)
	return s.scanGitHubApp(row)
}

// GetPrimaryGitHubApp returns the most recently configured GitHub App, or ErrNotFound if none exists.
func (s *Store) GetPrimaryGitHubApp() (*GitHubApp, error) {
	row := s.db.QueryRow(`SELECT `+githubAppCols+` FROM github_apps ORDER BY updated_at DESC LIMIT 1`)
	return s.scanGitHubApp(row)
}

// ListGitHubApps lists all configured GitHub Apps.
func (s *Store) ListGitHubApps() ([]GitHubApp, error) {
	rows, err := s.db.Query(`SELECT ` + githubAppCols + ` FROM github_apps ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []GitHubApp{}
	for rows.Next() {
		app, err := s.scanGitHubApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *app)
	}
	return out, rows.Err()
}

// DeleteGitHubApp deletes a saved GitHub App registration.
func (s *Store) DeleteGitHubApp(id string) error {
	res, err := s.db.Exec(`DELETE FROM github_apps WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FindAutoDeployServices returns all app services configured to auto-deploy a given repo and branch.
func (s *Store) FindAutoDeployServices(repoFullName, branch string) ([]Service, error) {
	rows, err := s.db.Query(`
		SELECT `+serviceSelectCols+`
		FROM services
		WHERE kind = 'app' AND auto_deploy = 1 AND LOWER(repo_full_name) = LOWER(?) AND repo_branch = ?
	`, repoFullName, branch)
	if err != nil {
		return nil, fmt.Errorf("find auto-deploy services: %w", err)
	}
	defer rows.Close()
	return scanServiceRows(rows)
}

const githubAppCols = `
	id, app_id, slug, name, html_url, client_id,
	client_secret_enc, webhook_secret_enc, private_key_enc,
	installation_id, account_login, account_type, created_at, updated_at
`

func (s *Store) scanGitHubApp(row rowScanner) (*GitHubApp, error) {
	var app GitHubApp
	var clientSecretEnc, webhookSecretEnc, privateKeyEnc []byte
	err := row.Scan(
		&app.ID, &app.AppID, &app.Slug, &app.Name, &app.HTMLURL, &app.ClientID,
		&clientSecretEnc, &webhookSecretEnc, &privateKeyEnc,
		&app.InstallationID, &app.AccountLogin, &app.AccountType, &app.CreatedAt, &app.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(clientSecretEnc) > 0 {
		if app.ClientSecret, err = s.vault.Decrypt(clientSecretEnc); err != nil {
			return nil, fmt.Errorf("decrypt client secret: %w", err)
		}
	}
	if len(webhookSecretEnc) > 0 {
		if app.WebhookSecret, err = s.vault.Decrypt(webhookSecretEnc); err != nil {
			return nil, fmt.Errorf("decrypt webhook secret: %w", err)
		}
	}
	if len(privateKeyEnc) > 0 {
		if app.PrivateKeyPEM, err = s.vault.Decrypt(privateKeyEnc); err != nil {
			return nil, fmt.Errorf("decrypt private key: %w", err)
		}
	}
	app.Connected = app.AppID > 0 && app.InstallationID > 0
	return &app, nil
}
