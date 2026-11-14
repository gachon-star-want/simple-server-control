export interface User {
  id: string;
  email: string;
  role: string;
}

export interface AuthStatus {
  needs_setup: boolean;
  authenticated: boolean;
  user?: User | null;
}

export interface Project {
  id: string;
  name: string;
  slug: string;
  description: string;
  network_name: string;
  created_at: string;
  updated_at: string;
}

export interface ProjectOverview extends Project {
  environment_count: number;
  service_count: number;
  running_count: number;
  failed_count: number;
  deploying_count: number;
  domains: string[];
  last_deployed_at?: string;
}

export interface Environment {
  id: string;
  project_id: string;
  name: string;
  slug: string;
}

export interface Domain {
  id: string;
  service_id: string;
  hostname: string;
  is_auto: boolean;
  https_enabled: boolean;
  dns_verified: boolean;
  last_checked_at?: string;
}

export interface Deployment {
  id: string;
  project_id: string;
  environment_id: string;
  service_id: string;
  commit_sha: string;
  commit_message: string;
  trigger_type: string;
  stage: string;
  status: string;
  image_tag: string;
  container_id: string;
  error_code: string;
  error_title_ko: string;
  error_detail_ko: string;
  started_at: string;
  finished_at?: string;
}

export interface Service {
  id: string;
  project_id: string;
  environment_id: string;
  name: string;
  slug: string;
  kind: "app" | "database";
  db_type: string;
  db_image: string;
  github_app_id: string;
  repo_full_name: string;
  repo_branch: string;
  root_dir: string;
  dockerfile_path: string;
  build_pack: string;
  port: number;
  healthcheck_path: string;
  healthcheck_timeout: number;
  auto_deploy: boolean;
  secrets_dirty: boolean;
  status: "idle" | "deploying" | "running" | "stopped" | "failed";
  active_container_id: string;
  active_container_name: string;
  active_image: string;
  volume_name: string;
  data_path: string;
  pos_x: number;
  pos_y: number;
}

export interface CanvasService extends Service {
  domains: Domain[];
  secret_count: number;
  running_count: number;
  latest_deployment?: Deployment;
}

export interface ServiceLink {
  id: string;
  project_id: string;
  environment_id: string;
  source_service_id: string;
  target_service_id: string;
  injected_env_key: string;
}

export interface ProjectCanvas {
  project: Project;
  environments: Environment[];
  active_environment: Environment;
  services: CanvasService[];
  links: ServiceLink[];
}

export interface SecretView {
  id: string;
  environment_id: string;
  service_id?: string;
  key: string;
  value: string;
  masked: boolean;
  is_sealed: boolean;
  is_system: boolean;
  updated_at: string;
}

export interface SecretInput {
  key: string;
  value: string;
  is_sealed: boolean;
  is_system?: boolean;
}

export interface GitHubApp {
  id: string;
  app_id: number;
  slug: string;
  name: string;
  html_url: string;
  installation_id: number;
  account_login: string;
  account_type: string;
  connected: boolean;
}

export interface RepoSummary {
  id: number;
  full_name: string;
  default_branch: string;
  private: boolean;
  html_url: string;
  description: string;
}

export interface DoctorCheck {
  name: string;
  status: "ok" | "warn" | "fail";
  message_ko: string;
  remediation_ko?: string;
}

export interface ContainerStats {
  cpu_percent: number;
  memory_bytes: number;
  memory_limit: number;
  memory_percent: number;
  net_rx_bytes: number;
  net_tx_bytes: number;
  pids: number;
}

export interface UpdateStatus {
  current_version: string;
  latest_version: string;
  update_available: boolean;
  is_dev_build: boolean;
  auto_update_enabled: boolean;
  auto_update_next_run?: string;
  systemd_managed: boolean;
  systemd_available: boolean;
  repo: string;
  check_error?: string;
}
