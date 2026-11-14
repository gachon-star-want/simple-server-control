package db

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// User represents an administrator account.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// GitHubApp holds GitHub App credentials (encrypted fields are excluded from JSON).
type GitHubApp struct {
	ID             string    `json:"id"`
	AppID          int64     `json:"app_id"`
	Slug           string    `json:"slug"`
	Name           string    `json:"name"`
	HTMLURL        string    `json:"html_url"`
	ClientID       string    `json:"client_id"`
	ClientSecret   string    `json:"-"`
	WebhookSecret  string    `json:"-"`
	PrivateKeyPEM  string    `json:"-"`
	InstallationID int64     `json:"installation_id"`
	AccountLogin   string    `json:"account_login"`
	AccountType    string    `json:"account_type"`
	Connected      bool      `json:"connected"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Project represents an isolated product workspace (dedicated Docker bridge network ssc-proj-<slug>).
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	NetworkName string    `json:"network_name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProjectOverview summarizes a project for the top-level Projects Hub.
type ProjectOverview struct {
	Project
	EnvironmentCount int        `json:"environment_count"`
	ServiceCount     int        `json:"service_count"`
	RunningCount     int        `json:"running_count"`
	FailedCount      int        `json:"failed_count"`
	DeployingCount   int        `json:"deploying_count"`
	Domains          []string   `json:"domains"`
	LastDeployedAt   *time.Time `json:"last_deployed_at,omitempty"`
}

// Environment belongs to a project (e.g., production, staging).
type Environment struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Service represents a deployable app container or managed database container inside an environment.
type Service struct {
	ID                 string    `json:"id"`
	ProjectID          string    `json:"project_id"`
	EnvironmentID      string    `json:"environment_id"`
	Name               string    `json:"name"`
	Slug               string    `json:"slug"`
	Kind               string    `json:"kind"` // "app" | "database"
	DBType             string    `json:"db_type"`
	DBImage            string    `json:"db_image"`
	GitHubAppID        string    `json:"github_app_id"`
	RepoFullName       string    `json:"repo_full_name"`
	RepoBranch         string    `json:"repo_branch"`
	RootDir            string    `json:"root_dir"`
	DockerfilePath     string    `json:"dockerfile_path"`
	BuildPack          string    `json:"build_pack"`
	Port               int       `json:"port"`
	HealthcheckPath    string    `json:"healthcheck_path"`
	HealthcheckTimeout int       `json:"healthcheck_timeout"`
	AutoDeploy         bool      `json:"auto_deploy"`
	SecretsDirty       bool      `json:"secrets_dirty"`
	Status             string    `json:"status"`
	StatusObservedAt    int64     `json:"status_observed_at"`
	ActiveContainerID   string    `json:"active_container_id"`
	ActiveContainerName string    `json:"active_container_name"`
	ActiveImage         string    `json:"active_image"`
	VolumeName         string    `json:"volume_name"`
	DataPath           string    `json:"data_path"` // app persistent folder; empty = stateless
	PosX               float64   `json:"pos_x"`
	PosY               float64   `json:"pos_y"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ServiceLink defines a visual & env-injection connection between two services in the same environment.
type ServiceLink struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	EnvironmentID   string    `json:"environment_id"`
	SourceServiceID string    `json:"source_service_id"`
	TargetServiceID string    `json:"target_service_id"`
	InjectedEnvKey  string    `json:"injected_env_key"`
	CreatedAt       time.Time `json:"created_at"`
}

// Domain binds an HTTP/HTTPS hostname routed by Caddy to an app service.
type Domain struct {
	ID            string     `json:"id"`
	ServiceID     string     `json:"service_id"`
	Hostname      string     `json:"hostname"`
	IsAuto        bool       `json:"is_auto"`
	HTTPSEnabled  bool       `json:"https_enabled"`
	DNSVerified   bool       `json:"dns_verified"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// SecretView is the API-safe representation of an environment secret.
// When Masked is true or IsSealed is true, Value contains only a mask placeholder.
type SecretView struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	ServiceID     *string   `json:"service_id,omitempty"`
	Key           string    `json:"key"`
	Value         string    `json:"value"`
	Masked        bool      `json:"masked"`
	IsSealed      bool      `json:"is_sealed"`
	IsSystem      bool      `json:"is_system"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SecretInput defines a secret key/value update request.
type SecretInput struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSealed bool   `json:"is_sealed"`
	IsSystem bool   `json:"is_system"`
}

// Deployment records one build & zero-downtime release execution.
type Deployment struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"project_id"`
	EnvironmentID string     `json:"environment_id"`
	ServiceID     string     `json:"service_id"`
	CommitSHA     string     `json:"commit_sha"`
	CommitMessage string     `json:"commit_message"`
	TriggerType   string     `json:"trigger_type"`
	Stage         string     `json:"stage"`
	Status        string     `json:"status"`
	ImageTag      string     `json:"image_tag"`
	ContainerID   string     `json:"container_id"`
	LogPath       string     `json:"log_path"`
	ErrorCode     string     `json:"error_code"`
	ErrorTitleKo  string     `json:"error_title_ko"`
	ErrorDetailKo string     `json:"error_detail_ko"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// CanvasService summarizes a service on the interactive Railway-style canvas without exposing secrets.
type CanvasService struct {
	Service
	Domains          []Domain    `json:"domains"`
	SecretCount      int         `json:"secret_count"`
	RunningCount     int         `json:"running_count"`
	LatestDeployment *Deployment `json:"latest_deployment,omitempty"`
}

// ProjectCanvas is the complete zero-secret graph payload rendered on the project canvas.
type ProjectCanvas struct {
	Project           Project         `json:"project"`
	Environments      []Environment   `json:"environments"`
	ActiveEnvironment Environment     `json:"active_environment"`
	Services          []CanvasService `json:"services"`
	Links             []ServiceLink   `json:"links"`
}

// MaskValue returns an obscured display string safe for UI previews.
func MaskValue(val string) string {
	if val == "" {
		return ""
	}
	return "••••••••"
}

// Slugify converts an arbitrary label (including Korean or mixed UTF-8) into a DNS/Docker-safe slug.
func Slugify(input, fallbackPrefix string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	var b strings.Builder
	prevDash := false
	for _, r := range lower {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		buf := make([]byte, 3)
		_, _ = rand.Read(buf)
		return fallbackPrefix + "-" + hex.EncodeToString(buf)
	}
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return slug
}
