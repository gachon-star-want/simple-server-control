// Package docker wraps Docker Engine v28 operations behind interfaces for offline testing
// and enforces per-project network isolation on Ubuntu VPS hosts.
package docker

import (
	"context"
	"io"
	"net"
	"time"
)

// Labels applied to every Docker resource managed by simple-server-control.
const (
	LabelManaged   = "ssc.managed"
	LabelProjectID = "ssc.project_id"
	LabelEnvID     = "ssc.environment_id"
	LabelServiceID = "ssc.service_id"
	LabelKind      = "ssc.kind"
)

// MountSpec describes either a named Docker volume or a host bind mount.
type MountSpec struct {
	Type   string // "volume" | "bind"
	Source string
	Target string
}

// PortBinding maps a container port to a host interface/port (reserved for ssc-caddy only).
type PortBinding struct {
	HostIP        string
	HostPort      string
	ContainerPort string
}

// ContainerSpec configures container creation without exposing app/DB host ports.
type ContainerSpec struct {
	Name           string
	Image          string
	Env            []string
	Cmd            []string
	ProjectNetwork string
	NetworkAliases []string
	AttachIngress  bool
	IngressNetwork string
	Labels         map[string]string
	Mounts         []MountSpec
	Ports          []PortBinding // Left empty for all user apps and databases.
	ExtraHosts     []string
	RestartPolicy  string // e.g., "unless-stopped"
	Privileged     bool   // Used only by internal BuildKit daemon container.
}

// ContainerInfo captures live Docker inspect metadata for a container.
type ContainerInfo struct {
	ID            string
	Name          string
	Running       bool
	Status        string
	OOMKilled     bool
	ExitCode      int
	HealthStatus  string
	StartedAt     time.Time
	IPByNetwork   map[string]string
}

// ContainerStats summarizes instantaneous resource metrics for one container.
type ContainerStats struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryBytes   uint64  `json:"memory_bytes"`
	MemoryLimit   uint64  `json:"memory_limit"`
	MemoryPercent float64 `json:"memory_percent"`
	NetRxBytes    uint64  `json:"net_rx_bytes"`
	NetTxBytes    uint64  `json:"net_tx_bytes"`
	PIDs          uint64  `json:"pids"`
}

// ExecSession wraps an interactive hijacked PTY exec connection inside a container.
type ExecSession struct {
	ExecID string
	Conn   net.Conn
	Reader io.Reader
	Resize func(rows, cols uint) error
	Close  func() error
}

// EngineEvent represents a normalized container lifecycle event from Docker Engine.
type EngineEvent struct {
	ContainerID string
	Action      string
	Attributes  map[string]string
	ObservedAt  int64
}

// Engine defines all Docker Engine operations required by deploy, proxy, logs, and terminal features.
type Engine interface {
	Ping(ctx context.Context) error
	EnsureNetwork(ctx context.Context, name string) error
	RemoveNetwork(ctx context.Context, name string) error
	EnsureVolume(ctx context.Context, name string) error
	PullImage(ctx context.Context, imageRef string, logOut io.Writer) error
	StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerInfo, error)
	StopContainer(ctx context.Context, containerID string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, containerID string) error
	InspectContainer(ctx context.Context, containerID string) (*ContainerInfo, error)
	ListManagedContainers(ctx context.Context, projectID string) ([]ContainerInfo, error)
	StreamLogs(ctx context.Context, containerID, tail string, follow bool) (io.ReadCloser, error)
	OneShotStats(ctx context.Context, containerID string) (*ContainerStats, error)
	OpenExec(ctx context.Context, containerID string, cmd []string, rows, cols uint) (*ExecSession, error)
	WatchEvents(ctx context.Context, handler func(EngineEvent)) error
	Close() error
}
