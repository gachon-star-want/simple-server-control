package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// Client implements Engine against the local Docker Engine socket using SDK v28.
type Client struct {
	cli *client.Client
}

// NewClient connects to Docker Engine via environment defaults (/var/run/docker.sock or DOCKER_HOST).
func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Raw exposes the underlying SDK client for diagnostic callers.
func (c *Client) Raw() *client.Client {
	return c.cli
}

// Close closes the underlying HTTP transport cleanly.
func (c *Client) Close() error {
	if c == nil || c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

// Ping verifies connectivity with Docker Engine.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

// EnsureNetwork idempotently creates an isolated bridge network labeled as SSC-managed.
func (c *Client) EnsureNetwork(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	_, err := c.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err == nil {
		return nil
	}
	if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspect network %s: %w", name, err)
	}
	_, err = c.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{LabelManaged: "true"},
	})
	if err != nil && !errdefs.IsConflict(err) {
		return fmt.Errorf("create network %s: %w", name, err)
	}
	return nil
}

// RemoveNetwork deletes a Docker network if it exists.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	err := c.cli.NetworkRemove(ctx, name)
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("remove network %s: %w", name, err)
	}
	return nil
}

// EnsureVolume idempotently creates a named Docker volume for persistent stateful storage.
func (c *Client) EnsureVolume(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	_, err := c.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: map[string]string{LabelManaged: "true"},
	})
	if err != nil {
		return fmt.Errorf("create volume %s: %w", name, err)
	}
	return nil
}

// PullImage pulls a container image and streams progress summaries into logOut when provided.
func (c *Client) PullImage(ctx context.Context, imageRef string, logOut io.Writer) error {
	rc, err := c.cli.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", imageRef, err)
	}
	defer rc.Close()

	if logOut == nil {
		_, err = io.Copy(io.Discard, rc)
		return err
	}

	scanner := bufio.NewScanner(rc)
	var lastLayer string
	for scanner.Scan() {
		var evt struct {
			Status string `json:"status"`
			ID     string `json:"id"`
			Error  string `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &evt) != nil {
			continue
		}
		if evt.Error != "" {
			return fmt.Errorf("pull %s: %s", imageRef, evt.Error)
		}
		line := evt.Status
		if evt.ID != "" && evt.ID != lastLayer && (evt.Status == "Pull complete" || evt.Status == "Already exists") {
			lastLayer = evt.ID
			_, _ = fmt.Fprintf(logOut, "[pull] %s: %s\n", evt.ID, line)
		}
	}
	return scanner.Err()
}

// StartContainer ensures networks/volumes, pulls missing images, creates and starts a container.
func (c *Client) StartContainer(ctx context.Context, spec ContainerSpec) (*ContainerInfo, error) {
	if _, _, err := c.cli.ImageInspectWithRaw(ctx, spec.Image); err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("inspect image %s: %w", spec.Image, err)
		}
		if err := c.PullImage(ctx, spec.Image, nil); err != nil {
			return nil, err
		}
	}

	if err := c.EnsureNetwork(ctx, spec.ProjectNetwork); err != nil {
		return nil, err
	}
	if spec.AttachIngress {
		if err := c.EnsureNetwork(ctx, spec.IngressNetwork); err != nil {
			return nil, err
		}
	}

	mounts := make([]mount.Mount, 0, len(spec.Mounts))
	for _, m := range spec.Mounts {
		mType := mount.TypeVolume
		if m.Type == "bind" {
			mType = mount.TypeBind
		} else {
			if err := c.EnsureVolume(ctx, m.Source); err != nil {
				return nil, err
			}
		}
		mounts = append(mounts, mount.Mount{
			Type:   mType,
			Source: m.Source,
			Target: m.Target,
		})
	}

	labels := map[string]string{LabelManaged: "true"}
	for k, v := range spec.Labels {
		labels[k] = v
	}

	cfg := &container.Config{
		Image:  spec.Image,
		Env:    spec.Env,
		Labels: labels,
	}
	if len(spec.Cmd) > 0 {
		cfg.Cmd = spec.Cmd
	}

	exposed := nat.PortSet{}
	portBindings := nat.PortMap{}
	for _, pb := range spec.Ports {
		p := nat.Port(pb.ContainerPort)
		exposed[p] = struct{}{}
		portBindings[p] = append(portBindings[p], nat.PortBinding{
			HostIP:   pb.HostIP,
			HostPort: pb.HostPort,
		})
	}
	if len(exposed) > 0 {
		cfg.ExposedPorts = exposed
	}

	restartMode := container.RestartPolicyUnlessStopped
	if spec.RestartPolicy == "no" {
		restartMode = container.RestartPolicyDisabled
	} else if spec.RestartPolicy == "always" {
		restartMode = container.RestartPolicyAlways
	}

	hostCfg := &container.HostConfig{
		Mounts:        mounts,
		PortBindings:  portBindings,
		ExtraHosts:    spec.ExtraHosts,
		Privileged:    spec.Privileged,
		RestartPolicy: container.RestartPolicy{Name: restartMode},
	}

	netCfg := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
	if spec.ProjectNetwork != "" {
		netCfg.EndpointsConfig[spec.ProjectNetwork] = &network.EndpointSettings{
			Aliases: spec.NetworkAliases,
		}
	} else if spec.AttachIngress && spec.IngressNetwork != "" {
		netCfg.EndpointsConfig[spec.IngressNetwork] = &network.EndpointSettings{
			Aliases: []string{spec.Name},
		}
	}

	created, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, spec.Name)
	if err != nil {
		return nil, fmt.Errorf("create container %s: %w", spec.Name, err)
	}

	if spec.ProjectNetwork != "" && spec.AttachIngress && spec.IngressNetwork != "" {
		err = c.cli.NetworkConnect(ctx, spec.IngressNetwork, created.ID, &network.EndpointSettings{
			Aliases: []string{spec.Name},
		})
		if err != nil {
			_ = c.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
			return nil, fmt.Errorf("connect container %s to ingress %s: %w", spec.Name, spec.IngressNetwork, err)
		}
	}

	if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		_ = c.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("start container %s: %w", spec.Name, err)
	}

	return c.InspectContainer(ctx, created.ID)
}

// StopContainer gracefully stops a container using the given timeout.
func (c *Client) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = 10
	}
	err := c.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &secs})
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("stop container %s: %w", containerID, err)
	}
	return nil
}

// RemoveContainer stops (if needed) and removes a container and anonymous volumes.
func (c *Client) RemoveContainer(ctx context.Context, containerID string) error {
	if containerID == "" {
		return nil
	}
	err := c.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: false})
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("remove container %s: %w", containerID, err)
	}
	return nil
}

// InspectContainer returns normalized runtime info for a container ID or name.
func (c *Client) InspectContainer(ctx context.Context, containerID string) (*ContainerInfo, error) {
	ins, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, err
	}
	info := &ContainerInfo{
		ID:          ins.ID,
		Name:        strings.TrimPrefix(ins.Name, "/"),
		IPByNetwork: make(map[string]string),
	}
	if ins.State != nil {
		info.Running = ins.State.Running
		info.Status = string(ins.State.Status)
		info.OOMKilled = ins.State.OOMKilled
		info.ExitCode = ins.State.ExitCode
		if ins.State.Health != nil {
			info.HealthStatus = string(ins.State.Health.Status)
		}
		if t, parseErr := time.Parse(time.RFC3339Nano, ins.State.StartedAt); parseErr == nil {
			info.StartedAt = t
		}
	}
	if ins.NetworkSettings != nil {
		for netName, ep := range ins.NetworkSettings.Networks {
			if ep != nil && ep.IPAddress != "" {
				info.IPByNetwork[netName] = ep.IPAddress
			}
		}
	}
	return info, nil
}

// ListManagedContainers lists containers labeled with ssc.managed=true (optionally filtered by projectID).
func (c *Client) ListManagedContainers(ctx context.Context, projectID string) ([]ContainerInfo, error) {
	f := filters.NewArgs(filters.Arg("label", LabelManaged+"=true"))
	if projectID != "" {
		f.Add("label", LabelProjectID+"="+projectID)
	}
	items, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	out := make([]ContainerInfo, 0, len(items))
	for _, it := range items {
		name := ""
		if len(it.Names) > 0 {
			name = strings.TrimPrefix(it.Names[0], "/")
		}
		ips := make(map[string]string)
		if it.NetworkSettings != nil {
			for netName, ep := range it.NetworkSettings.Networks {
				if ep != nil && ep.IPAddress != "" {
					ips[netName] = ep.IPAddress
				}
			}
		}
		out = append(out, ContainerInfo{
			ID:          it.ID,
			Name:        name,
			Running:     it.State == "running",
			Status:      it.State,
			IPByNetwork: ips,
		})
	}
	return out, nil
}

// StreamLogs demultiplexes container stdout and stderr into a single readable stream.
func (c *Client) StreamLogs(ctx context.Context, containerID, tail string, follow bool) (io.ReadCloser, error) {
	if tail == "" {
		tail = "200"
	}
	ins, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, err
	}
	raw, err := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
		Timestamps: true,
	})
	if err != nil {
		return nil, err
	}
	if ins.Config != nil && ins.Config.Tty {
		return raw, nil
	}

	pr, pw := io.Pipe()
	go func() {
		defer raw.Close()
		_, copyErr := stdcopy.StdCopy(pw, pw, raw)
		_ = pw.CloseWithError(copyErr)
	}()
	return pr, nil
}

// OneShotStats reads container CPU%, memory footprint, network I/O, and PID count.
func (c *Client) OneShotStats(ctx context.Context, containerID string) (*ContainerStats, error) {
	resp, err := c.cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	stats := &ContainerStats{
		MemoryLimit: raw.MemoryStats.Limit,
		PIDs:        raw.PidsStats.Current,
	}

	memUsage := raw.MemoryStats.Usage
	if cache := raw.MemoryStats.Stats["inactive_file"]; cache > 0 && memUsage > cache {
		memUsage -= cache
	} else if cache := raw.MemoryStats.Stats["cache"]; cache > 0 && memUsage > cache {
		memUsage -= cache
	}
	stats.MemoryBytes = memUsage
	if raw.MemoryStats.Limit > 0 {
		stats.MemoryPercent = float64(memUsage) / float64(raw.MemoryStats.Limit) * 100.0
	}

	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemUsage - raw.PreCPUStats.SystemUsage)
	if cpuDelta > 0 && sysDelta > 0 {
		online := float64(raw.CPUStats.OnlineCPUs)
		if online == 0 {
			online = float64(len(raw.CPUStats.CPUUsage.PercpuUsage))
		}
		if online == 0 {
			online = 1
		}
		stats.CPUPercent = (cpuDelta / sysDelta) * online * 100.0
	}

	for _, ns := range raw.Networks {
		stats.NetRxBytes += ns.RxBytes
		stats.NetTxBytes += ns.TxBytes
	}
	return stats, nil
}

// OpenExec starts an interactive PTY exec session inside a running container.
func (c *Client) OpenExec(ctx context.Context, containerID string, cmd []string, rows, cols uint) (*ExecSession, error) {
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"}
	}
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}

	created, err := c.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		ConsoleSize:  &[2]uint{rows, cols},
		Cmd:          cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	attached, err := c.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{
		Tty:         true,
		ConsoleSize: &[2]uint{rows, cols},
	})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}

	return &ExecSession{
		ExecID: created.ID,
		Conn:   attached.Conn,
		Reader: attached.Reader,
		Resize: func(r, cols uint) error {
			return c.cli.ContainerExecResize(ctx, created.ID, container.ResizeOptions{
				Height: r,
				Width:  cols,
			})
		},
		Close: func() error {
			attached.Close()
			return nil
		},
	}, nil
}

// WatchEvents subscribes to ssc.managed container lifecycle events until ctx is canceled.
func (c *Client) WatchEvents(ctx context.Context, handler func(EngineEvent)) error {
	f := filters.NewArgs(
		filters.Arg("type", string(events.ContainerEventType)),
		filters.Arg("label", LabelManaged+"=true"),
	)
	msgCh, errCh := c.cli.Events(ctx, events.ListOptions{Filters: f})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return err
			}
			return nil
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			ts := msg.TimeNano
			if ts == 0 {
				ts = time.Now().UnixNano()
			}
			handler(EngineEvent{
				ContainerID: msg.Actor.ID,
				Action:      string(msg.Action),
				Attributes:  msg.Actor.Attributes,
				ObservedAt:  ts,
			})
		}
	}
}
