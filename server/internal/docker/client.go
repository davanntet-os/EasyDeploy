// Package docker wraps the Docker Engine SDK with the narrow set of
// operations EasyDeploy needs: inspecting and controlling containers,
// streaming logs and stats, pulling images, and creating deployments with
// a label that ties each container back to its EasyDeploy subdomain.
package docker

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// LabelSubdomain marks a container as managed by EasyDeploy and records the
// subdomain the proxy should route to it.
const LabelSubdomain = "easydeploy.subdomain"

// LabelManaged flags containers created through EasyDeploy.
const LabelManaged = "easydeploy.managed"

// LabelOwner records the username that owns a container, used to attribute
// resource usage against a member's quota.
const LabelOwner = "easydeploy.owner"

// LabelService and LabelReplica tie a container to a load-balanced service
// and its replica index.
const (
	LabelService = "easydeploy.service"
	LabelReplica = "easydeploy.replica"
)

// Client is a thin, purpose-built wrapper over the Docker SDK client.
type Client struct {
	cli *client.Client

	// volHelpers caches long-lived helper containers (one per volume, keyed by
	// volume name) used for fast file operations — see volumes.go.
	volMu      sync.Mutex
	volHelpers map[string]string
}

// New constructs a Docker client. If host is empty the SDK resolves the
// endpoint from the environment or the default socket. API version is
// negotiated so we work across daemon versions.
func New(host string) (*Client, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{cli: cli, volHelpers: map[string]string{}}, nil
}

// Close releases the underlying connection.
func (c *Client) Close() error { return c.cli.Close() }

// Ping verifies the daemon is reachable.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

// ServerVersion returns the daemon's version string.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	v, err := c.cli.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// ListContainers returns container summaries. When all is false only running
// containers are returned.
func (c *Client) ListContainers(ctx context.Context, all bool) ([]types.Container, error) {
	return c.cli.ContainerList(ctx, container.ListOptions{All: all})
}

// Inspect returns the full inspect payload for a container.
func (c *Client) Inspect(ctx context.Context, id string) (types.ContainerJSON, error) {
	return c.cli.ContainerInspect(ctx, id)
}

// Start starts a stopped container.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.cli.ContainerStart(ctx, id, container.StartOptions{})
}

// Stop stops a running container using the daemon's default timeout.
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.cli.ContainerStop(ctx, id, container.StopOptions{})
}

// Restart restarts a container.
func (c *Client) Restart(ctx context.Context, id string) error {
	return c.cli.ContainerRestart(ctx, id, container.StopOptions{})
}

// Remove force-removes a container.
func (c *Client) Remove(ctx context.Context, id string) error {
	return c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

// Logs returns a stream of the container's logs. When follow is true the
// stream stays open and emits new lines as they arrive. The returned stream
// is multiplexed (stdout+stderr) using Docker's stream framing.
func (c *Client) Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error) {
	return c.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       "200",
		Timestamps: false,
	})
}

// Stats returns a stream of resource-usage samples for a container.
func (c *Client) Stats(ctx context.Context, id string, stream bool) (io.ReadCloser, error) {
	resp, err := c.cli.ContainerStats(ctx, id, stream)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// ListImages returns all local images.
func (c *Client) ListImages(ctx context.Context) ([]image.Summary, error) {
	return c.cli.ImageList(ctx, image.ListOptions{All: false})
}

// PullImage pulls an image, returning the progress stream the caller must
// drain and close.
func (c *Client) PullImage(ctx context.Context, ref string) (io.ReadCloser, error) {
	return c.PullImageAuth(ctx, ref, "")
}

// PullImageAuth pulls an image using an optional base64-encoded registry auth
// token (see registry.EncodeAuth).
func (c *Client) PullImageAuth(ctx context.Context, ref, auth string) (io.ReadCloser, error) {
	return c.cli.ImagePull(ctx, ref, image.PullOptions{RegistryAuth: auth})
}

// RemoveImage deletes a local image.
func (c *Client) RemoveImage(ctx context.Context, id string, force bool) error {
	_, err := c.cli.ImageRemove(ctx, id, image.RemoveOptions{Force: force, PruneChildren: true})
	return err
}

// ListVolumes returns all volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]*volume.Volume, error) {
	resp, err := c.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, err
	}
	return resp.Volumes, nil
}

// ListNetworks returns all networks.
func (c *Client) ListNetworks(ctx context.Context) ([]network.Summary, error) {
	return c.cli.NetworkList(ctx, network.ListOptions{})
}

// DeploySpec describes a container EasyDeploy should create and start.
type DeploySpec struct {
	Name      string   // container name (also used as default subdomain)
	Image     string   // image reference, e.g. "nginx:latest"
	Env       []string // KEY=VALUE pairs
	Subdomain string   // subdomain to route via Envoy (may be empty)
	// ContainerPort is the port inside the container that serves HTTP; it is
	// the target the proxy routes to. Required when a subdomain is set.
	ContainerPort int
	// Publish maps host ports to container ports for direct access (optional).
	Publish map[string]string // "hostPort" -> "containerPort/proto"
	Network string            // optional network to attach
	// RegistryAuth is an optional base64-encoded auth token for pulling from
	// a private registry (see registry.EncodeAuth).
	RegistryAuth string
	// Owner is the username this container is attributed to (may be empty for
	// admin deploys).
	Owner string
	// NanoCPUs caps CPU as billionths of a core (1e9 = 1 core). 0 = unlimited.
	NanoCPUs int64
	// MemoryBytes caps memory. 0 = unlimited.
	MemoryBytes int64
	// ExtraLabels are merged onto the container's labels (e.g. service/replica).
	ExtraLabels map[string]string
	// AdvancedSpec exposes the full breadth of container options (mounts,
	// ports, capabilities, restart policy, healthcheck, …).
	AdvancedSpec
}

// Deploy pulls the image if needed, creates the container with EasyDeploy
// labels, starts it, and returns the new container ID.
func (c *Client) Deploy(ctx context.Context, spec DeploySpec) (string, error) {
	// Ensure the image is present. Errors from pull are surfaced only if the
	// subsequent create fails, since the image may already be local.
	if rc, err := c.PullImageAuth(ctx, spec.Image, spec.RegistryAuth); err == nil {
		_, _ = io.Copy(io.Discard, rc)
		_ = rc.Close()
	}

	labels := map[string]string{LabelManaged: "true"}
	if spec.Subdomain != "" {
		labels[LabelSubdomain] = spec.Subdomain
	}
	if spec.Owner != "" {
		labels[LabelOwner] = spec.Owner
	}
	for k, v := range spec.ExtraLabels {
		labels[k] = v
	}

	exposed, bindings, err := buildPorts(spec)
	if err != nil {
		return "", err
	}

	cfg := &container.Config{
		Image:        spec.Image,
		Env:          spec.Env,
		Labels:       labels,
		ExposedPorts: exposed,
	}
	hostCfg := &container.HostConfig{
		PortBindings:  bindings,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Resources: container.Resources{
			NanoCPUs: spec.NanoCPUs,
			Memory:   spec.MemoryBytes,
		},
	}
	// Apply the full advanced option set (mounts, extra ports, caps, etc.).
	if err := applyAdvanced(cfg, hostCfg, spec.AdvancedSpec); err != nil {
		return "", fmt.Errorf("apply options: %w", err)
	}
	var netCfg *network.NetworkingConfig
	if spec.Network != "" {
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{spec.Network: {}},
		}
	}

	created, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, spec.Name)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}
	return created.ID, nil
}

func buildPorts(spec DeploySpec) (nat.PortSet, nat.PortMap, error) {
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	if spec.ContainerPort > 0 {
		p, err := nat.NewPort("tcp", fmt.Sprintf("%d", spec.ContainerPort))
		if err != nil {
			return nil, nil, err
		}
		exposed[p] = struct{}{}
	}
	for hostPort, containerSpec := range spec.Publish {
		proto := "tcp"
		portStr := containerSpec
		if i := indexByte(containerSpec, '/'); i >= 0 {
			portStr = containerSpec[:i]
			proto = containerSpec[i+1:]
		}
		p, err := nat.NewPort(proto, portStr)
		if err != nil {
			return nil, nil, err
		}
		exposed[p] = struct{}{}
		bindings[p] = append(bindings[p], nat.PortBinding{HostIP: "0.0.0.0", HostPort: hostPort})
	}
	return exposed, bindings, nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
