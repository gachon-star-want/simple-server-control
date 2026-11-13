import type {
  AuthStatus,
  ContainerStats,
  Deployment,
  DoctorCheck,
  Domain,
  Environment,
  GitHubApp,
  Project,
  ProjectCanvas,
  ProjectOverview,
  RepoSummary,
  SecretInput,
  SecretView,
  Service,
  ServiceLink,
} from "../types";

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(message: string, status: number, code = "API_ERROR") {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
    ...init,
  });
  const text = await resp.text();
  const data = text ? JSON.parse(text) : {};
  if (!resp.ok) {
    throw new ApiError(data.error || `요청 실패 (${resp.status})`, resp.status, data.code);
  }
  return data as T;
}

export function wsURL(path: string): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}${path}`;
}

export const api = {
  authStatus: () => request<AuthStatus>("/api/auth/status"),
  setup: (body: { setup_token: string; email: string; password: string }) =>
    request<{ user: AuthStatus["user"] }>("/api/auth/setup", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  login: (body: { email: string; password: string }) =>
    request<{ user: AuthStatus["user"] }>("/api/auth/login", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  logout: () => request<{ ok: boolean }>("/api/auth/logout", { method: "POST" }),

  listProjects: () => request<{ projects: ProjectOverview[] }>("/api/projects"),
  createProject: (body: { name: string; description: string }) =>
    request<{ project: Project; environment: Environment }>("/api/projects", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  deleteProject: (id: string) =>
    request<{ ok: boolean }>(`/api/projects/${id}`, { method: "DELETE" }),

  getCanvas: (projectId: string, envId?: string) =>
    request<ProjectCanvas>(
      `/api/projects/${projectId}/canvas${envId ? `?env_id=${encodeURIComponent(envId)}` : ""}`
    ),
  createEnvironment: (projectId: string, name: string) =>
    request<{ environment: Environment }>(`/api/projects/${projectId}/environments`, {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  savePositions: (
    projectId: string,
    environment_id: string,
    positions: Array<{ service_id: string; pos_x: number; pos_y: number }>
  ) =>
    request<{ ok: boolean }>(`/api/projects/${projectId}/canvas/positions`, {
      method: "PUT",
      body: JSON.stringify({ environment_id, positions }),
    }),
  createLink: (
    projectId: string,
    body: {
      environment_id: string;
      source_service_id: string;
      target_service_id: string;
      injected_env_key: string;
    }
  ) =>
    request<{ link: ServiceLink }>(`/api/projects/${projectId}/links`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  deleteLink: (projectId: string, linkId: string) =>
    request<{ ok: boolean }>(`/api/projects/${projectId}/links/${linkId}`, { method: "DELETE" }),

  createService: (
    projectId: string,
    body: {
      environment_id: string;
      name: string;
      kind: "app" | "database";
      db_type?: string;
      github_app_id?: string;
      repo_full_name?: string;
      repo_branch?: string;
      root_dir?: string;
      build_pack?: string;
      port?: number;
      healthcheck_path?: string;
      auto_deploy?: boolean;
      auto_domain?: boolean;
      deploy_now?: boolean;
    }
  ) =>
    request<{
      service: Service;
      domain?: Domain;
      deployment?: Deployment;
      provision_warning?: string;
    }>(`/api/projects/${projectId}/services`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  updateService: (serviceId: string, patch: Partial<Service>) =>
    request<{ service: Service }>(`/api/services/${serviceId}`, {
      method: "PATCH",
      body: JSON.stringify(patch),
    }),
  deleteService: (serviceId: string) =>
    request<{ ok: boolean }>(`/api/services/${serviceId}`, { method: "DELETE" }),

  deployService: (serviceId: string) =>
    request<{ deployment: Deployment }>(`/api/services/${serviceId}/deploy`, { method: "POST" }),
  restartService: (serviceId: string) =>
    request<{ ok: boolean }>(`/api/services/${serviceId}/restart`, { method: "POST" }),
  stopService: (serviceId: string) =>
    request<{ ok: boolean }>(`/api/services/${serviceId}/stop`, { method: "POST" }),
  listDeployments: (serviceId: string) =>
    request<{ deployments: Deployment[] }>(`/api/services/${serviceId}/deployments`),
  rollbackService: (serviceId: string, deployment_id: string) =>
    request<{ ok: boolean }>(`/api/services/${serviceId}/rollback`, {
      method: "POST",
      body: JSON.stringify({ deployment_id }),
    }),
  getServiceStats: (serviceId: string) =>
    request<{ stats: ContainerStats | null; error?: string }>(`/api/services/${serviceId}/stats`),
  getServiceLogs: (serviceId: string, tail = 200) =>
    request<{ logs: string }>(`/api/services/${serviceId}/logs?tail=${tail}`),

  listServiceSecrets: (serviceId: string, reveal = false) =>
    request<{ secrets: SecretView[] }>(`/api/services/${serviceId}/secrets?reveal=${reveal}`),
  saveServiceSecrets: (serviceId: string, secrets: SecretInput[]) =>
    request<{ ok: boolean }>(`/api/services/${serviceId}/secrets`, {
      method: "PUT",
      body: JSON.stringify({ secrets }),
    }),
  importServiceDotEnv: (serviceId: string, raw: string, is_sealed = false) =>
    request<{ imported: number }>(`/api/services/${serviceId}/secrets/import-env`, {
      method: "POST",
      body: JSON.stringify({ raw, is_sealed }),
    }),

  listSharedSecrets: (envId: string, reveal = false) =>
    request<{ secrets: SecretView[] }>(`/api/environments/${envId}/secrets?reveal=${reveal}`),
  saveSharedSecrets: (envId: string, secrets: SecretInput[]) =>
    request<{ ok: boolean }>(`/api/environments/${envId}/secrets`, {
      method: "PUT",
      body: JSON.stringify({ secrets }),
    }),
  importSharedDotEnv: (envId: string, raw: string, is_sealed = false) =>
    request<{ imported: number }>(`/api/environments/${envId}/secrets/import-env`, {
      method: "POST",
      body: JSON.stringify({ raw, is_sealed }),
    }),

  listDomains: (serviceId: string) =>
    request<{ domains: Domain[] }>(`/api/services/${serviceId}/domains`),
  addDomain: (serviceId: string, hostname: string, https_enabled = true) =>
    request<{ domain: Domain; server_ip: string; dns_message: string }>(
      `/api/services/${serviceId}/domains`,
      {
        method: "POST",
        body: JSON.stringify({ hostname, https_enabled }),
      }
    ),
  verifyDomain: (domainId: string) =>
    request<{
      domain: Domain;
      verified: boolean;
      resolved_ips?: string[];
      server_ip: string;
      message: string;
    }>(`/api/domains/${domainId}/verify`, { method: "POST" }),
  deleteDomain: (domainId: string) =>
    request<{ ok: boolean }>(`/api/domains/${domainId}`, { method: "DELETE" }),

  githubStatus: () =>
    request<{ apps: GitHubApp[]; detected_origin: string }>("/api/github/status"),
  createGitHubManifest: (body: { app_name: string; public_origin: string; organization?: string }) =>
    request<{ action_url: string; manifest: string; state: string }>("/api/github/manifest", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  listGitHubRepos: () =>
    request<{ repositories: RepoSummary[]; app_id: string }>("/api/github/repos"),
  listGitHubBranches: (repo: string) =>
    request<{ branches: string[] }>(`/api/github/branches?repo=${encodeURIComponent(repo)}`),
  deleteGitHubApp: (id: string) =>
    request<{ ok: boolean }>(`/api/github/app?id=${encodeURIComponent(id)}`, { method: "DELETE" }),

  systemDoctor: () => request<{ checks: DoctorCheck[] }>("/api/system/doctor"),
  systemInfo: () =>
    request<{ panel_domain: string; public_origin: string; base_domain: string }>("/api/system/info"),
  setPanelDomain: (panel_domain: string) =>
    request<{ panel_domain: string }>("/api/system/panel-domain", {
      method: "PUT",
      body: JSON.stringify({ panel_domain }),
    }),
};

export function statusLabelKo(status: string): string {
  switch (status) {
    case "running":
      return "실행 중";
    case "deploying":
      return "배포 중";
    case "stopped":
      return "중지됨";
    case "failed":
      return "오류";
    default:
      return "대기 중";
  }
}

export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let idx = 0;
  let val = bytes;
  while (val >= 1024 && idx < units.length - 1) {
    val /= 1024;
    idx++;
  }
  return `${val.toFixed(idx === 0 ? 0 : 1)} ${units[idx]}`;
}
