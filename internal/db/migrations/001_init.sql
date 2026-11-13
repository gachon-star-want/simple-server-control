-- Initial SQLite schema for simple-server-control (WAL + foreign keys enforced by connection PRAGMA).

CREATE TABLE IF NOT EXISTS system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'admin',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS github_apps (
    id TEXT PRIMARY KEY,
    app_id INTEGER NOT NULL DEFAULT 0,
    slug TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    client_id TEXT NOT NULL DEFAULT '',
    client_secret_enc BLOB,
    webhook_secret_enc BLOB,
    private_key_enc BLOB,
    installation_id INTEGER NOT NULL DEFAULT 0,
    account_login TEXT NOT NULL DEFAULT '',
    account_type TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    network_name TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS environments (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(project_id, slug)
);

CREATE TABLE IF NOT EXISTS services (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('app', 'database')),
    db_type TEXT NOT NULL DEFAULT '',
    db_image TEXT NOT NULL DEFAULT '',
    github_app_id TEXT REFERENCES github_apps(id) ON DELETE SET NULL,
    repo_full_name TEXT NOT NULL DEFAULT '',
    repo_branch TEXT NOT NULL DEFAULT 'main',
    root_dir TEXT NOT NULL DEFAULT '.',
    dockerfile_path TEXT NOT NULL DEFAULT '',
    build_pack TEXT NOT NULL DEFAULT 'auto',
    port INTEGER NOT NULL DEFAULT 3000,
    healthcheck_path TEXT NOT NULL DEFAULT '/health',
    healthcheck_timeout INTEGER NOT NULL DEFAULT 30,
    auto_deploy INTEGER NOT NULL DEFAULT 1,
    secrets_dirty INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'idle',
    status_observed_at INTEGER NOT NULL DEFAULT 0,
    active_container_id TEXT NOT NULL DEFAULT '',
    active_container_name TEXT NOT NULL DEFAULT '',
    active_image TEXT NOT NULL DEFAULT '',
    volume_name TEXT NOT NULL DEFAULT '',
    pos_x REAL NOT NULL DEFAULT 0,
    pos_y REAL NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(environment_id, slug)
);

CREATE TABLE IF NOT EXISTS service_links (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    source_service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    target_service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    injected_env_key TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(environment_id, source_service_id, target_service_id, injected_env_key)
);

CREATE TABLE IF NOT EXISTS domains (
    id TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    hostname TEXT NOT NULL UNIQUE,
    is_auto INTEGER NOT NULL DEFAULT 0,
    https_enabled INTEGER NOT NULL DEFAULT 1,
    dns_verified INTEGER NOT NULL DEFAULT 0,
    last_checked_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS secrets (
    id TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    service_id TEXT REFERENCES services(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value_enc BLOB NOT NULL,
    is_sealed INTEGER NOT NULL DEFAULT 0,
    is_system INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_secrets_shared_key
    ON secrets(environment_id, key) WHERE service_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_secrets_service_key
    ON secrets(environment_id, service_id, key) WHERE service_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS deployments (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL DEFAULT '',
    commit_message TEXT NOT NULL DEFAULT '',
    trigger_type TEXT NOT NULL DEFAULT 'manual',
    stage TEXT NOT NULL DEFAULT 'queued',
    status TEXT NOT NULL DEFAULT 'running',
    image_tag TEXT NOT NULL DEFAULT '',
    container_id TEXT NOT NULL DEFAULT '' ,
    log_path TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    error_title_ko TEXT NOT NULL DEFAULT '',
    error_detail_ko TEXT NOT NULL DEFAULT '',
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_environments_project ON environments(project_id);
CREATE INDEX IF NOT EXISTS idx_services_project ON services(project_id);
CREATE INDEX IF NOT EXISTS idx_services_env ON services(environment_id);
CREATE INDEX IF NOT EXISTS idx_services_repo ON services(repo_full_name, repo_branch);
CREATE INDEX IF NOT EXISTS idx_service_links_env ON service_links(environment_id);
CREATE INDEX IF NOT EXISTS idx_domains_service ON domains(service_id);
CREATE INDEX IF NOT EXISTS idx_secrets_env_service ON secrets(environment_id, service_id);
CREATE INDEX IF NOT EXISTS idx_deployments_service_started ON deployments(service_id, started_at DESC);
