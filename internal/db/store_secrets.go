package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/envref"
	"github.com/google/uuid"
)

// ListSecrets returns masked or unmasked secret views for an environment (or specific service when serviceID != "").
// Sealed secrets remain masked even when reveal is true.
func (s *Store) ListSecrets(envID, serviceID string, reveal bool) ([]SecretView, error) {
	var rows *sql.Rows
	var err error
	if serviceID == "" {
		rows, err = s.db.Query(`
			SELECT id, environment_id, service_id, key, value_enc, is_sealed, is_system, updated_at
			FROM secrets
			WHERE environment_id = ? AND service_id IS NULL
			ORDER BY key ASC
		`, envID)
	} else {
		rows, err = s.db.Query(`
			SELECT id, environment_id, service_id, key, value_enc, is_sealed, is_system, updated_at
			FROM secrets
			WHERE environment_id = ? AND service_id = ?
			ORDER BY key ASC
		`, envID, serviceID)
	}
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	defer rows.Close()

	out := []SecretView{}
	for rows.Next() {
		var sv SecretView
		var svcID sql.NullString
		var enc []byte
		var isSealed, isSystem int
		if err := rows.Scan(&sv.ID, &sv.EnvironmentID, &svcID, &sv.Key, &enc, &isSealed, &isSystem, &sv.UpdatedAt); err != nil {
			return nil, err
		}
		if svcID.Valid {
			v := svcID.String
			sv.ServiceID = &v
		}
		sv.IsSealed = isSealed == 1
		sv.IsSystem = isSystem == 1

		plain, err := s.vault.Decrypt(enc)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %s: %w", sv.Key, err)
		}

		if reveal && !sv.IsSealed {
			sv.Value = plain
			sv.Masked = false
		} else {
			sv.Value = MaskValue(plain)
			sv.Masked = true
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

// UpsertSecret encrypts and stores a single shared or service-scoped secret.
func (s *Store) UpsertSecret(envID, serviceID string, in SecretInput) error {
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return fmt.Errorf("secret key is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.upsertSecretTx(tx, envID, serviceID, in); err != nil {
		return err
	}
	s.markSecretsDirtyTx(tx, envID, serviceID)
	return tx.Commit()
}

// ReplaceSecretsBulk reconciles the editable set of secrets for a service or shared env.
// Existing sealed secrets whose value stays Masked placeholder are preserved intact.
func (s *Store) ReplaceSecretsBulk(envID, serviceID string, inputs []SecretInput) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	existingKeys, err := loadExistingSecretMetaTx(tx, envID, serviceID)
	if err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(inputs))
	for _, in := range inputs {
		key := strings.TrimSpace(in.Key)
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
		if meta, ok := existingKeys[key]; ok && meta.isSealed && in.Value == "••••••••" {
			// Preserve sealed secret value untouched when editing other keys.
			continue
		}
		if err := s.upsertSecretTx(tx, envID, serviceID, in); err != nil {
			return err
		}
	}

	for key, meta := range existingKeys {
		if _, ok := seen[key]; ok {
			continue
		}
		if meta.isSystem {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM secrets WHERE id = ?`, meta.id); err != nil {
			return err
		}
	}

	s.markSecretsDirtyTx(tx, envID, serviceID)
	return tx.Commit()
}

// DeleteSecret removes one secret by key from the given scope.
func (s *Store) DeleteSecret(envID, serviceID, key string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var res sql.Result
	if serviceID == "" {
		res, err = tx.Exec(`DELETE FROM secrets WHERE environment_id = ? AND service_id IS NULL AND key = ?`, envID, key)
	} else {
		res, err = tx.Exec(`DELETE FROM secrets WHERE environment_id = ? AND service_id = ? AND key = ?`, envID, serviceID, key)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.markSecretsDirtyTx(tx, envID, serviceID)
	return tx.Commit()
}

// ResolveServiceRuntimeEnv decrypts and resolves shared + service + ${{service.KEY}} variables for container start.
// Returned map MUST NEVER be returned directly from canvas/summary APIs.
func (s *Store) ResolveServiceRuntimeEnv(serviceID string) (map[string]string, error) {
	svc, err := s.GetService(serviceID)
	if err != nil {
		return nil, err
	}

	sharedPlain, err := s.decryptScopeMap(svc.EnvironmentID, "")
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT id, slug FROM services WHERE environment_id = ?`, svc.EnvironmentID)
	if err != nil {
		return nil, err
	}
	svcSlugByID := make(map[string]string)
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			_ = rows.Close()
			return nil, err
		}
		svcSlugByID[id] = slug
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	svcVarsBySlug := make(map[string]map[string]string, len(svcSlugByID))
	for id, slug := range svcSlugByID {
		vars, err := s.decryptScopeMap(svc.EnvironmentID, id)
		if err != nil {
			return nil, err
		}
		svcVarsBySlug[slug] = vars
	}

	// Ensure explicit service_links automatically expose linked target connection vars if not overridden.
	linkRows, err := s.db.Query(`
		SELECT target_service_id, injected_env_key
		FROM service_links
		WHERE environment_id = ? AND source_service_id = ?
	`, svc.EnvironmentID, svc.ID)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()

	for linkRows.Next() {
		var targetID, envKey string
		if err := linkRows.Scan(&targetID, &envKey); err != nil {
			return nil, err
		}
		envKey = strings.TrimSpace(envKey)
		if envKey == "" {
			continue
		}
		targetSlug, ok := svcSlugByID[targetID]
		if !ok {
			continue
		}
		if _, exists := svcVarsBySlug[svc.Slug][envKey]; !exists {
			if targetVars := svcVarsBySlug[targetSlug]; targetVars != nil {
				if _, hasConn := targetVars["CONNECTION_URL"]; hasConn {
					svcVarsBySlug[svc.Slug][envKey] = fmt.Sprintf("${{%s.CONNECTION_URL}}", targetSlug)
				} else if _, hasDB := targetVars["DATABASE_URL"]; hasDB {
					svcVarsBySlug[svc.Slug][envKey] = fmt.Sprintf("${{%s.DATABASE_URL}}", targetSlug)
				}
			}
		}
	}

	return envref.ResolveServiceEnv(svc.Slug, sharedPlain, svcVarsBySlug)
}

func (s *Store) decryptScopeMap(envID, serviceID string) (map[string]string, error) {
	var rows *sql.Rows
	var err error
	if serviceID == "" {
		rows, err = s.db.Query(`SELECT key, value_enc FROM secrets WHERE environment_id = ? AND service_id IS NULL`, envID)
	} else {
		rows, err = s.db.Query(`SELECT key, value_enc FROM secrets WHERE environment_id = ? AND service_id = ?`, envID, serviceID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var k string
		var enc []byte
		if err := rows.Scan(&k, &enc); err != nil {
			return nil, err
		}
		plain, err := s.vault.Decrypt(enc)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %s: %w", k, err)
		}
		out[k] = plain
	}
	return out, rows.Err()
}

type secretMeta struct {
	id       string
	isSealed bool
	isSystem bool
}

func loadExistingSecretMetaTx(tx *sql.Tx, envID, serviceID string) (map[string]secretMeta, error) {
	var rows *sql.Rows
	var err error
	if serviceID == "" {
		rows, err = tx.Query(`SELECT id, key, is_sealed, is_system FROM secrets WHERE environment_id = ? AND service_id IS NULL`, envID)
	} else {
		rows, err = tx.Query(`SELECT id, key, is_sealed, is_system FROM secrets WHERE environment_id = ? AND service_id = ?`, envID, serviceID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]secretMeta)
	for rows.Next() {
		var k string
		var m secretMeta
		var sealed, sys int
		if err := rows.Scan(&m.id, &k, &sealed, &sys); err != nil {
			return nil, err
		}
		m.isSealed = sealed == 1
		m.isSystem = sys == 1
		out[k] = m
	}
	return out, rows.Err()
}

func (s *Store) upsertSecretTx(tx *sql.Tx, envID, serviceID string, in SecretInput) error {
	key := strings.TrimSpace(in.Key)
	enc, err := s.vault.Encrypt(in.Value)
	if err != nil {
		return fmt.Errorf("encrypt secret %s: %w", key, err)
	}

	var existingID string
	if serviceID == "" {
		err = tx.QueryRow(`SELECT id FROM secrets WHERE environment_id = ? AND service_id IS NULL AND key = ?`, envID, key).Scan(&existingID)
	} else {
		err = tx.QueryRow(`SELECT id FROM secrets WHERE environment_id = ? AND service_id = ? AND key = ?`, envID, serviceID, key).Scan(&existingID)
	}

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if existingID != "" {
		_, err = tx.Exec(`
			UPDATE secrets
			SET value_enc = ?, is_sealed = CASE WHEN is_sealed = 1 THEN 1 ELSE ? END, is_system = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, enc, boolToInt(in.IsSealed), boolToInt(in.IsSystem), existingID)
		return err
	}

	var svcID sql.NullString
	if serviceID != "" {
		svcID = sql.NullString{String: serviceID, Valid: true}
	}
	_, err = tx.Exec(`
		INSERT INTO secrets (id, environment_id, service_id, key, value_enc, is_sealed, is_system, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, uuid.NewString(), envID, svcID, key, enc, boolToInt(in.IsSealed), boolToInt(in.IsSystem))
	return err
}

func (s *Store) markSecretsDirtyTx(tx *sql.Tx, envID, serviceID string) {
	if serviceID != "" {
		_, _ = tx.Exec(`UPDATE services SET secrets_dirty = 1 WHERE id = ? AND active_container_id != ''`, serviceID)
		return
	}
	_, _ = tx.Exec(`UPDATE services SET secrets_dirty = 1 WHERE environment_id = ? AND active_container_id != ''`, envID)
}

// CreateDeployment records the start of a new deployment run.
func (s *Store) CreateDeployment(projectID, envID, serviceID, triggerType, commitSHA, commitMsg, logPath string) (*Deployment, error) {
	now := time.Now().UTC()
	d := &Deployment{
		ID:            uuid.NewString(),
		ProjectID:     projectID,
		EnvironmentID: envID,
		ServiceID:     serviceID,
		CommitSHA:     commitSHA,
		CommitMessage: commitMsg,
		TriggerType:   triggerType,
		Stage:         "cloning",
		Status:        "running",
		LogPath:       logPath,
		StartedAt:     now,
	}
	_, err := s.db.Exec(`
		INSERT INTO deployments (
			id, project_id, environment_id, service_id, commit_sha, commit_message,
			trigger_type, stage, status, image_tag, container_id, log_path,
			error_code, error_title_ko, error_detail_ko, started_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, '', '', '', ?)
	`, d.ID, d.ProjectID, d.EnvironmentID, d.ServiceID, d.CommitSHA, d.CommitMessage,
		d.TriggerType, d.Stage, d.Status, d.LogPath, d.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	return d, nil
}

// UpdateDeploymentStage updates the current zero-downtime pipeline stage and optional image/container references.
func (s *Store) UpdateDeploymentStage(deploymentID, stage, imageTag, containerID string) error {
	_, err := s.db.Exec(`
		UPDATE deployments SET
			stage = ?,
			image_tag = CASE WHEN ? != '' THEN ? ELSE image_tag END,
			container_id = CASE WHEN ? != '' THEN ? ELSE container_id END
		WHERE id = ?
	`, stage, imageTag, imageTag, containerID, containerID, deploymentID)
	return err
}

// CompleteDeployment marks a deployment as succeeded.
func (s *Store) CompleteDeployment(deploymentID, imageTag, containerID string) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		UPDATE deployments SET
			stage = 'succeeded',
			status = 'succeeded',
			image_tag = CASE WHEN ? != '' THEN ? ELSE image_tag END,
			container_id = CASE WHEN ? != '' THEN ? ELSE container_id END,
			finished_at = ?
		WHERE id = ?
	`, imageTag, imageTag, containerID, containerID, now, deploymentID)
	return err
}

// FailDeployment records a structured failure with Korean diagnostic messages.
func (s *Store) FailDeployment(deploymentID, stage, code, titleKo, detailKo string) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		UPDATE deployments SET
			stage = ?,
			status = 'failed',
			error_code = ?,
			error_title_ko = ?,
			error_detail_ko = ?,
			finished_at = ?
		WHERE id = ?
	`, stage, code, titleKo, detailKo, now, deploymentID)
	return err
}

// GetDeployment fetches a single deployment record by ID.
func (s *Store) GetDeployment(id string) (*Deployment, error) {
	row := s.db.QueryRow(`
		SELECT id, project_id, environment_id, service_id, commit_sha, commit_message,
		       trigger_type, stage, status, image_tag, container_id, log_path,
		       error_code, error_title_ko, error_detail_ko, started_at, finished_at
		FROM deployments WHERE id = ?
	`, id)
	d, err := scanDeployment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// GetLatestDeploymentByService fetches the most recent deployment of a service.
func (s *Store) GetLatestDeploymentByService(serviceID string) (*Deployment, error) {
	row := s.db.QueryRow(`
		SELECT id, project_id, environment_id, service_id, commit_sha, commit_message,
		       trigger_type, stage, status, image_tag, container_id, log_path,
		       error_code, error_title_ko, error_detail_ko, started_at, finished_at
		FROM deployments WHERE service_id = ? ORDER BY started_at DESC LIMIT 1
	`, serviceID)
	d, err := scanDeployment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// ListDeploymentsByService returns recent deployment history for a service.
func (s *Store) ListDeploymentsByService(serviceID string, limit int) ([]Deployment, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, project_id, environment_id, service_id, commit_sha, commit_message,
		       trigger_type, stage, status, image_tag, container_id, log_path,
		       error_code, error_title_ko, error_detail_ko, started_at, finished_at
		FROM deployments WHERE service_id = ? ORDER BY started_at DESC LIMIT ?
	`, serviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func scanDeployment(row rowScanner) (*Deployment, error) {
	var d Deployment
	var finishedAt sql.NullTime
	err := row.Scan(
		&d.ID, &d.ProjectID, &d.EnvironmentID, &d.ServiceID, &d.CommitSHA, &d.CommitMessage,
		&d.TriggerType, &d.Stage, &d.Status, &d.ImageTag, &d.ContainerID, &d.LogPath,
		&d.ErrorCode, &d.ErrorTitleKo, &d.ErrorDetailKo, &d.StartedAt, &finishedAt,
	)
	if err != nil {
		return nil, err
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		d.FinishedAt = &t
	}
	return &d, nil
}
