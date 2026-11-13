package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CreateServiceInput defines fields required to create an app or database service.
type CreateServiceInput struct {
	ProjectID          string  `json:"project_id"`
	EnvironmentID      string  `json:"environment_id"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"` // "app" | "database"
	DBType             string  `json:"db_type"`
	DBImage            string  `json:"db_image"`
	GitHubAppID        string  `json:"github_app_id"`
	RepoFullName       string  `json:"repo_full_name"`
	RepoBranch         string  `json:"repo_branch"`
	RootDir            string  `json:"root_dir"`
	DockerfilePath     string  `json:"dockerfile_path"`
	BuildPack          string  `json:"build_pack"`
	Port               int     `json:"port"`
	HealthcheckPath    string  `json:"healthcheck_path"`
	HealthcheckTimeout int     `json:"healthcheck_timeout"`
	AutoDeploy         *bool   `json:"auto_deploy,omitempty"`
	VolumeName         string  `json:"volume_name"`
	PosX               float64 `json:"pos_x"`
	PosY               float64 `json:"pos_y"`
}

// UpdateServiceInput defines mutable configuration fields of a service.
type UpdateServiceInput struct {
	Name               *string `json:"name,omitempty"`
	RepoBranch         *string `json:"repo_branch,omitempty"`
	RootDir            *string `json:"root_dir,omitempty"`
	DockerfilePath     *string `json:"dockerfile_path,omitempty"`
	BuildPack          *string `json:"build_pack,omitempty"`
	Port               *int    `json:"port,omitempty"`
	HealthcheckPath    *string `json:"healthcheck_path,omitempty"`
	HealthcheckTimeout *int    `json:"healthcheck_timeout,omitempty"`
	AutoDeploy         *bool   `json:"auto_deploy,omitempty"`
}

// RouteTarget pairs a verified domain with its upstream Docker DNS target for Caddy routing.
type RouteTarget struct {
	Domain              Domain
	ProjectSlug         string
	NetworkName         string
	ServiceID           string
	ServiceSlug         string
	ActiveContainerName string
	Port                int
	Active              bool
}

// ServicePosition records a canvas coordinate update for one service node.
type ServicePosition struct {
	ServiceID string  `json:"service_id"`
	PosX      float64 `json:"pos_x"`
	PosY      float64 `json:"pos_y"`
}

// CreateProject creates a new isolated project and its default production environment.
func (s *Store) CreateProject(name, description string) (*Project, *Environment, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, fmt.Errorf("project name is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("begin create project: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	slug, err := ensureUniqueSlug(tx, `SELECT COUNT(1) FROM projects WHERE slug = ?`, Slugify(name, "project"))
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	p := &Project{
		ID:          uuid.NewString(),
		Name:        name,
		Slug:        slug,
		Description: strings.TrimSpace(description),
		NetworkName: "ssc-proj-" + slug,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := tx.Exec(`
		INSERT INTO projects (id, name, slug, description, network_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.Name, p.Slug, p.Description, p.NetworkName, p.CreatedAt, p.UpdatedAt); err != nil {
		return nil, nil, fmt.Errorf("insert project: %w", err)
	}

	env := &Environment{
		ID:        uuid.NewString(),
		ProjectID: p.ID,
		Name:      "production",
		Slug:      "production",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := tx.Exec(`
		INSERT INTO environments (id, project_id, name, slug, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, env.ID, env.ProjectID, env.Name, env.Slug, env.CreatedAt, env.UpdatedAt); err != nil {
		return nil, nil, fmt.Errorf("insert default environment: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit create project: %w", err)
	}
	return p, env, nil
}

// ListProjectOverviews returns all projects enriched with container/service counts and attached domains.
func (s *Store) ListProjectOverviews() ([]ProjectOverview, error) {
	rows, err := s.db.Query(`
		SELECT id, name, slug, description, network_name, created_at, updated_at
		FROM projects ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []ProjectOverview
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &p.NetworkName, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, ProjectOverview{Project: p, Domains: []string{}})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		if err := s.fillProjectOverview(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) fillProjectOverview(ov *ProjectOverview) error {
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM environments WHERE project_id = ?`, ov.ID).Scan(&ov.EnvironmentCount); err != nil {
		return fmt.Errorf("count environments: %w", err)
	}
	if err := s.db.QueryRow(`
		SELECT
			COUNT(1),
			COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'deploying' THEN 1 ELSE 0 END), 0)
		FROM services WHERE project_id = ?
	`, ov.ID).Scan(&ov.ServiceCount, &ov.RunningCount, &ov.FailedCount, &ov.DeployingCount); err != nil {
		return fmt.Errorf("count project services: %w", err)
	}

	domRows, err := s.db.Query(`
		SELECT d.hostname
		FROM domains d
		JOIN services svc ON svc.id = d.service_id
		WHERE svc.project_id = ?
		ORDER BY d.is_auto ASC, d.hostname ASC
	`, ov.ID)
	if err != nil {
		return fmt.Errorf("list project domains: %w", err)
	}
	defer domRows.Close()
	for domRows.Next() {
		var host string
		if err := domRows.Scan(&host); err != nil {
			return err
		}
		ov.Domains = append(ov.Domains, host)
	}

	var lastAt sql.NullTime
	if err := s.db.QueryRow(`SELECT MAX(started_at) FROM deployments WHERE project_id = ?`, ov.ID).Scan(&lastAt); err != nil {
		return fmt.Errorf("query last deployment: %w", err)
	}
	if lastAt.Valid {
		t := lastAt.Time
		ov.LastDeployedAt = &t
	}
	return nil
}

// GetProject fetches a single project by ID.
func (s *Store) GetProject(id string) (*Project, error) {
	var p Project
	err := s.db.QueryRow(`
		SELECT id, name, slug, description, network_name, created_at, updated_at
		FROM projects WHERE id = ?
	`, id).Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &p.NetworkName, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project %s: %w", id, err)
	}
	return &p, nil
}

// DeleteProject deletes a project and cascades to all child records.
func (s *Store) DeleteProject(id string) error {
	res, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete project %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateEnvironment adds a new named environment inside a project.
func (s *Store) CreateEnvironment(projectID, name string) (*Environment, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("environment name is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	slug, err := ensureUniqueSlugInScope(tx, `SELECT COUNT(1) FROM environments WHERE project_id = ? AND slug = ?`, projectID, Slugify(name, "env"))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	env := &Environment{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		Name:      name,
		Slug:      slug,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := tx.Exec(`
		INSERT INTO environments (id, project_id, name, slug, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, env.ID, env.ProjectID, env.Name, env.Slug, env.CreatedAt, env.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert environment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return env, nil
}

// ListEnvironments returns all environments belonging to a project.
func (s *Store) ListEnvironments(projectID string) ([]Environment, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, name, slug, created_at, updated_at
		FROM environments WHERE project_id = ?
		ORDER BY CASE WHEN slug = 'production' THEN 0 ELSE 1 END, created_at ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	defer rows.Close()

	var out []Environment
	for rows.Next() {
		var e Environment
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Name, &e.Slug, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEnvironment fetches a single environment by ID.
func (s *Store) GetEnvironment(id string) (*Environment, error) {
	var e Environment
	err := s.db.QueryRow(`
		SELECT id, project_id, name, slug, created_at, updated_at
		FROM environments WHERE id = ?
	`, id).Scan(&e.ID, &e.ProjectID, &e.Name, &e.Slug, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get environment %s: %w", id, err)
	}
	return &e, nil
}

// CreateService creates a new app or database service node in the target project environment.
func (s *Store) CreateService(in CreateServiceInput) (*Service, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if in.Kind != "app" && in.Kind != "database" {
		return nil, fmt.Errorf("invalid service kind %q", in.Kind)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	slug, err := ensureUniqueSlugInScope(tx, `SELECT COUNT(1) FROM services WHERE environment_id = ? AND slug = ?`, in.EnvironmentID, Slugify(in.Name, "svc"))
	if err != nil {
		return nil, err
	}

	posX, posY := in.PosX, in.PosY
	if posX == 0 && posY == 0 {
		var count int
		_ = tx.QueryRow(`SELECT COUNT(1) FROM services WHERE environment_id = ?`, in.EnvironmentID).Scan(&count)
		posX = 360 + float64((count%3)*310)
		posY = 120 + float64((count/3)*220)
	}

	port := in.Port
	if port <= 0 && in.Kind == "app" {
		port = 3000
	}
	branch := strings.TrimSpace(in.RepoBranch)
	if branch == "" {
		branch = "main"
	}
	rootDir := strings.TrimSpace(in.RootDir)
	if rootDir == "" {
		rootDir = "."
	}
	buildPack := strings.TrimSpace(in.BuildPack)
	if buildPack == "" {
		buildPack = "auto"
	}
	hcPath := strings.TrimSpace(in.HealthcheckPath)
	if hcPath == "" && in.Kind == "app" {
		hcPath = "/health"
	}
	hcTimeout := in.HealthcheckTimeout
	if hcTimeout <= 0 {
		hcTimeout = 30
	}
	autoDeploy := true
	if in.AutoDeploy != nil {
		autoDeploy = *in.AutoDeploy
	}

	now := time.Now().UTC()
	svc := &Service{
		ID:                 uuid.NewString(),
		ProjectID:          in.ProjectID,
		EnvironmentID:      in.EnvironmentID,
		Name:               in.Name,
		Slug:               slug,
		Kind:               in.Kind,
		DBType:             in.DBType,
		DBImage:            in.DBImage,
		GitHubAppID:        strings.TrimSpace(in.GitHubAppID),
		RepoFullName:       strings.TrimSpace(in.RepoFullName),
		RepoBranch:         branch,
		RootDir:            rootDir,
		DockerfilePath:     strings.TrimSpace(in.DockerfilePath),
		BuildPack:          buildPack,
		Port:               port,
		HealthcheckPath:    hcPath,
		HealthcheckTimeout: hcTimeout,
		AutoDeploy:         autoDeploy,
		Status:             "idle",
		VolumeName:         in.VolumeName,
		PosX:               posX,
		PosY:               posY,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	var ghAppID sql.NullString
	if svc.GitHubAppID != "" {
		ghAppID = sql.NullString{String: svc.GitHubAppID, Valid: true}
	}

	if _, err := tx.Exec(`
		INSERT INTO services (
			id, project_id, environment_id, name, slug, kind, db_type, db_image,
			github_app_id, repo_full_name, repo_branch, root_dir, dockerfile_path,
			build_pack, port, healthcheck_path, healthcheck_timeout, auto_deploy,
			secrets_dirty, status, status_observed_at, active_container_id, active_container_name, active_image,
			volume_name, pos_x, pos_y, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 'idle', 0, '', '', '', ?, ?, ?, ?, ?)
	`,
		svc.ID, svc.ProjectID, svc.EnvironmentID, svc.Name, svc.Slug, svc.Kind, svc.DBType, svc.DBImage,
		ghAppID, svc.RepoFullName, svc.RepoBranch, svc.RootDir, svc.DockerfilePath,
		svc.BuildPack, svc.Port, svc.HealthcheckPath, svc.HealthcheckTimeout, boolToInt(svc.AutoDeploy),
		svc.VolumeName, svc.PosX, svc.PosY, svc.CreatedAt, svc.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert service: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return svc, nil
}

// GetService fetches a single service by ID.
func (s *Store) GetService(id string) (*Service, error) {
	row := s.db.QueryRow(`SELECT `+serviceSelectCols+` FROM services WHERE id = ?`, id)
	svc, err := scanService(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get service %s: %w", id, err)
	}
	return svc, nil
}

// ListServicesByProject returns all services across all environments of a project.
func (s *Store) ListServicesByProject(projectID string) ([]Service, error) {
	rows, err := s.db.Query(`SELECT `+serviceSelectCols+` FROM services WHERE project_id = ? ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project services: %w", err)
	}
	defer rows.Close()
	return scanServiceRows(rows)
}

// UpdateService updates mutable settings of an existing service.
func (s *Store) UpdateService(id string, in UpdateServiceInput) (*Service, error) {
	svc, err := s.GetService(id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		svc.Name = strings.TrimSpace(*in.Name)
	}
	if in.RepoBranch != nil {
		svc.RepoBranch = strings.TrimSpace(*in.RepoBranch)
	}
	if in.RootDir != nil {
		svc.RootDir = strings.TrimSpace(*in.RootDir)
	}
	if in.DockerfilePath != nil {
		svc.DockerfilePath = strings.TrimSpace(*in.DockerfilePath)
	}
	if in.BuildPack != nil {
		svc.BuildPack = strings.TrimSpace(*in.BuildPack)
	}
	if in.Port != nil && *in.Port > 0 {
		svc.Port = *in.Port
	}
	if in.HealthcheckPath != nil {
		svc.HealthcheckPath = strings.TrimSpace(*in.HealthcheckPath)
	}
	if in.HealthcheckTimeout != nil && *in.HealthcheckTimeout > 0 {
		svc.HealthcheckTimeout = *in.HealthcheckTimeout
	}
	if in.AutoDeploy != nil {
		svc.AutoDeploy = *in.AutoDeploy
	}

	if _, err := s.db.Exec(`
		UPDATE services SET
			name = ?, repo_branch = ?, root_dir = ?, dockerfile_path = ?,
			build_pack = ?, port = ?, healthcheck_path = ?, healthcheck_timeout = ?,
			auto_deploy = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, svc.Name, svc.RepoBranch, svc.RootDir, svc.DockerfilePath,
		svc.BuildPack, svc.Port, svc.HealthcheckPath, svc.HealthcheckTimeout,
		boolToInt(svc.AutoDeploy), id); err != nil {
		return nil, fmt.Errorf("update service %s: %w", id, err)
	}
	return s.GetService(id)
}

// UpdateServicePositions persists batch canvas coordinates for services in an environment.
func (s *Store) UpdateServicePositions(envID string, positions []ServicePosition) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`UPDATE services SET pos_x = ?, pos_y = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND environment_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, pos := range positions {
		if _, err := stmt.Exec(pos.PosX, pos.PosY, pos.ServiceID, envID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateServiceRuntimeStatus updates live container status guarded by status_observed_at ordering.
func (s *Store) UpdateServiceRuntimeStatus(serviceID, status, containerID, containerName, activeImage string, observedAt int64, clearDirty bool) (bool, error) {
	query := `
		UPDATE services SET
			status = ?,
			active_container_id = CASE WHEN ? != '' THEN ? ELSE active_container_id END,
			active_container_name = CASE WHEN ? != '' THEN ? ELSE active_container_name END,
			active_image = CASE WHEN ? != '' THEN ? ELSE active_image END,
			status_observed_at = ?,
			secrets_dirty = CASE WHEN ? = 1 THEN 0 ELSE secrets_dirty END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status_observed_at <= ?
	`
	res, err := s.db.Exec(query, status, containerID, containerID, containerName, containerName, activeImage, activeImage, observedAt, boolToInt(clearDirty), serviceID, observedAt)
	if err != nil {
		return false, fmt.Errorf("update runtime status for %s: %w", serviceID, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteService removes a service by ID.
func (s *Store) DeleteService(id string) error {
	res, err := s.db.Exec(`DELETE FROM services WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete service %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateServiceLink connects two services within the same environment and marks source secrets dirty if running.
func (s *Store) CreateServiceLink(projectID, envID, sourceID, targetID, injectedEnvKey string) (*ServiceLink, error) {
	if sourceID == targetID {
		return nil, fmt.Errorf("cannot link a service to itself")
	}
	source, err := s.GetService(sourceID)
	if err != nil {
		return nil, err
	}
	target, err := s.GetService(targetID)
	if err != nil {
		return nil, err
	}
	if source.EnvironmentID != envID || target.EnvironmentID != envID {
		return nil, fmt.Errorf("linked services must belong to the same environment")
	}

	link := &ServiceLink{
		ID:              uuid.NewString(),
		ProjectID:       projectID,
		EnvironmentID:   envID,
		SourceServiceID: sourceID,
		TargetServiceID: targetID,
		InjectedEnvKey:  strings.TrimSpace(injectedEnvKey),
		CreatedAt:       time.Now().UTC(),
	}
	if _, err := s.db.Exec(`
		INSERT INTO service_links (id, project_id, environment_id, source_service_id, target_service_id, injected_env_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, link.ID, link.ProjectID, link.EnvironmentID, link.SourceServiceID, link.TargetServiceID, link.InjectedEnvKey, link.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert service link: %w", err)
	}
	if source.ActiveContainerID != "" {
		_, _ = s.db.Exec(`UPDATE services SET secrets_dirty = 1 WHERE id = ?`, source.ID)
	}
	return link, nil
}

// DeleteServiceLink removes a visual/env connection edge between services.
func (s *Store) DeleteServiceLink(id string) error {
	res, err := s.db.Exec(`DELETE FROM service_links WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete service link %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateDomain attaches a custom or auto domain to an app service.
func (s *Store) CreateDomain(serviceID, hostname string, isAuto, httpsEnabled, dnsVerified bool) (*Domain, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return nil, fmt.Errorf("hostname is required")
	}
	now := time.Now().UTC()
	d := &Domain{
		ID:           uuid.NewString(),
		ServiceID:    serviceID,
		Hostname:     hostname,
		IsAuto:       isAuto,
		HTTPSEnabled: httpsEnabled,
		DNSVerified:  dnsVerified,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := s.db.Exec(`
		INSERT INTO domains (id, service_id, hostname, is_auto, https_enabled, dns_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, d.ID, d.ServiceID, d.Hostname, boolToInt(d.IsAuto), boolToInt(d.HTTPSEnabled), boolToInt(d.DNSVerified), d.CreatedAt, d.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert domain %s: %w", hostname, err)
	}
	return d, nil
}

// UpdateDomainStatus updates DNS verification and HTTPS flags for a domain.
func (s *Store) UpdateDomainStatus(id string, dnsVerified, httpsEnabled bool) (*Domain, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`
		UPDATE domains
		SET dns_verified = ?, https_enabled = ?, last_checked_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, boolToInt(dnsVerified), boolToInt(httpsEnabled), now, id)
	if err != nil {
		return nil, fmt.Errorf("update domain %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetDomain(id)
}

// GetDomain fetches a domain record by ID.
func (s *Store) GetDomain(id string) (*Domain, error) {
	var d Domain
	var isAuto, httpsEnabled, dnsVerified int
	var lastChecked sql.NullTime
	err := s.db.QueryRow(`
		SELECT id, service_id, hostname, is_auto, https_enabled, dns_verified, last_checked_at, created_at, updated_at
		FROM domains WHERE id = ?
	`, id).Scan(&d.ID, &d.ServiceID, &d.Hostname, &isAuto, &httpsEnabled, &dnsVerified, &lastChecked, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.IsAuto = isAuto == 1
	d.HTTPSEnabled = httpsEnabled == 1
	d.DNSVerified = dnsVerified == 1
	if lastChecked.Valid {
		t := lastChecked.Time
		d.LastCheckedAt = &t
	}
	return &d, nil
}

// DeleteDomain removes a domain binding.
func (s *Store) DeleteDomain(id string) error {
	res, err := s.db.Exec(`DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDomainsByService returns all domains attached to a service.
func (s *Store) ListDomainsByService(serviceID string) ([]Domain, error) {
	rows, err := s.db.Query(`
		SELECT id, service_id, hostname, is_auto, https_enabled, dns_verified, last_checked_at, created_at, updated_at
		FROM domains WHERE service_id = ? ORDER BY is_auto ASC, created_at ASC
	`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Domain
	for rows.Next() {
		var d Domain
		var isAuto, httpsEnabled, dnsVerified int
		var lastChecked sql.NullTime
		if err := rows.Scan(&d.ID, &d.ServiceID, &d.Hostname, &isAuto, &httpsEnabled, &dnsVerified, &lastChecked, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.IsAuto = isAuto == 1
		d.HTTPSEnabled = httpsEnabled == 1
		d.DNSVerified = dnsVerified == 1
		if lastChecked.Valid {
			t := lastChecked.Time
			d.LastCheckedAt = &t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListAllRouteTargets returns every domain with its target container DNS metadata for Caddy sync.
func (s *Store) ListAllRouteTargets() ([]RouteTarget, error) {
	rows, err := s.db.Query(`
		SELECT
			d.id, d.service_id, d.hostname, d.is_auto, d.https_enabled, d.dns_verified, d.last_checked_at, d.created_at, d.updated_at,
			p.slug, p.network_name, svc.slug, svc.active_container_name, svc.port, svc.status
		FROM domains d
		JOIN services svc ON svc.id = d.service_id
		JOIN projects p ON p.id = svc.project_id
		WHERE svc.kind = 'app'
		ORDER BY d.hostname ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list route targets: %w", err)
	}
	defer rows.Close()

	var out []RouteTarget
	for rows.Next() {
		var rt RouteTarget
		var isAuto, httpsEnabled, dnsVerified int
		var lastChecked sql.NullTime
		var status string
		if err := rows.Scan(
			&rt.Domain.ID, &rt.Domain.ServiceID, &rt.Domain.Hostname, &isAuto, &httpsEnabled, &dnsVerified, &lastChecked, &rt.Domain.CreatedAt, &rt.Domain.UpdatedAt,
			&rt.ProjectSlug, &rt.NetworkName, &rt.ServiceSlug, &rt.ActiveContainerName, &rt.Port, &status,
		); err != nil {
			return nil, err
		}
		rt.Domain.IsAuto = isAuto == 1
		rt.Domain.HTTPSEnabled = httpsEnabled == 1
		rt.Domain.DNSVerified = dnsVerified == 1
		if lastChecked.Valid {
			t := lastChecked.Time
			rt.Domain.LastCheckedAt = &t
		}
		rt.ServiceID = rt.Domain.ServiceID
		rt.Active = status == "running" || status == "deploying"
		out = append(out, rt)
	}
	return out, rows.Err()
}

// GetProjectCanvas builds the complete zero-secret canvas view for a project and active environment.
func (s *Store) GetProjectCanvas(projectID, envID string) (*ProjectCanvas, error) {
	proj, err := s.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	envs, err := s.ListEnvironments(projectID)
	if err != nil {
		return nil, err
	}
	if len(envs) == 0 {
		return nil, fmt.Errorf("project %s has no environments", projectID)
	}

	activeEnv := envs[0]
	if envID != "" {
		found := false
		for _, env := range envs {
			if env.ID == envID {
				activeEnv = env
				found = true
				break
			}
		}
		if !found {
			return nil, ErrNotFound
		}
	}

	rows, err := s.db.Query(`SELECT `+serviceSelectCols+` FROM services WHERE environment_id = ? ORDER BY created_at ASC`, activeEnv.ID)
	if err != nil {
		return nil, fmt.Errorf("list env services: %w", err)
	}
	defer rows.Close()
	services, err := scanServiceRows(rows)
	if err != nil {
		return nil, err
	}

	canvasServices := make([]CanvasService, 0, len(services))
	for _, svc := range services {
		domains, err := s.ListDomainsByService(svc.ID)
		if err != nil {
			return nil, err
		}
		if domains == nil {
			domains = []Domain{}
		}
		var secretCount int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM secrets WHERE environment_id = ? AND service_id = ?`, activeEnv.ID, svc.ID).Scan(&secretCount); err != nil {
			return nil, err
		}
		latest, err := s.GetLatestDeploymentByService(svc.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		runningCount := 0
		if svc.Status == "running" {
			runningCount = 1
		}
		canvasServices = append(canvasServices, CanvasService{
			Service:          svc,
			Domains:          domains,
			SecretCount:      secretCount,
			RunningCount:     runningCount,
			LatestDeployment: latest,
		})
	}

	linkRows, err := s.db.Query(`
		SELECT id, project_id, environment_id, source_service_id, target_service_id, injected_env_key, created_at
		FROM service_links WHERE environment_id = ? ORDER BY created_at ASC
	`, activeEnv.ID)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()

	links := []ServiceLink{}
	for linkRows.Next() {
		var l ServiceLink
		if err := linkRows.Scan(&l.ID, &l.ProjectID, &l.EnvironmentID, &l.SourceServiceID, &l.TargetServiceID, &l.InjectedEnvKey, &l.CreatedAt); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	if err := linkRows.Err(); err != nil {
		return nil, err
	}

	return &ProjectCanvas{
		Project:           *proj,
		Environments:      envs,
		ActiveEnvironment: activeEnv,
		Services:          canvasServices,
		Links:             links,
	}, nil
}

const serviceSelectCols = `
	id, project_id, environment_id, name, slug, kind, db_type, db_image,
	github_app_id, repo_full_name, repo_branch, root_dir, dockerfile_path,
	build_pack, port, healthcheck_path, healthcheck_timeout, auto_deploy,
	secrets_dirty, status, status_observed_at, active_container_id, active_container_name, active_image,
	volume_name, pos_x, pos_y, created_at, updated_at
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanService(row rowScanner) (*Service, error) {
	var svc Service
	var ghAppID sql.NullString
	var autoDeploy, dirty int
	err := row.Scan(
		&svc.ID, &svc.ProjectID, &svc.EnvironmentID, &svc.Name, &svc.Slug, &svc.Kind, &svc.DBType, &svc.DBImage,
		&ghAppID, &svc.RepoFullName, &svc.RepoBranch, &svc.RootDir, &svc.DockerfilePath,
		&svc.BuildPack, &svc.Port, &svc.HealthcheckPath, &svc.HealthcheckTimeout, &autoDeploy,
		&dirty, &svc.Status, &svc.StatusObservedAt, &svc.ActiveContainerID, &svc.ActiveContainerName, &svc.ActiveImage,
		&svc.VolumeName, &svc.PosX, &svc.PosY, &svc.CreatedAt, &svc.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if ghAppID.Valid {
		svc.GitHubAppID = ghAppID.String
	}
	svc.AutoDeploy = autoDeploy == 1
	svc.SecretsDirty = dirty == 1
	return &svc, nil
}

func scanServiceRows(rows *sql.Rows) ([]Service, error) {
	var out []Service
	for rows.Next() {
		svc, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *svc)
	}
	return out, rows.Err()
}

func ensureUniqueSlug(tx *sql.Tx, countQuery, base string) (string, error) {
	candidate := base
	for suffix := 2; ; suffix++ {
		var n int
		if err := tx.QueryRow(countQuery, candidate).Scan(&n); err != nil {
			return "", fmt.Errorf("check slug uniqueness: %w", err)
		}
		if n == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
}

func ensureUniqueSlugInScope(tx *sql.Tx, countQuery, scopeID, base string) (string, error) {
	candidate := base
	for suffix := 2; ; suffix++ {
		var n int
		if err := tx.QueryRow(countQuery, scopeID, candidate).Scan(&n); err != nil {
			return "", fmt.Errorf("check scoped slug uniqueness: %w", err)
		}
		if n == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
}

// CountActiveRecentDeployments returns active deployments started within maxAge.
func (s *Store) CountActiveRecentDeployments(maxAge time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM deployments
		WHERE status IN ('queued', 'running') AND finished_at IS NULL AND started_at >= ?
	`, cutoff).Scan(&n)
	return n, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
