package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const (
	edgeEnvoyImage = "envoyproxy/envoy:v1.31-latest"
	// EdgeContainerName is the fixed name of the edge Envoy on a host.
	EdgeContainerName = "easydeploy-edge-proxy"
	// EdgeNetwork is the Docker network the edge proxy and managed containers
	// share so Envoy can resolve replica container names (STRICT_DNS).
	EdgeNetwork = "easydeploy-edge"
	// LabelEdge flags the edge Envoy container.
	LabelEdge          = "easydeploy.edge"
	edgeBootstrapPath  = "/bootstrap.yaml"
	edgeContainerPort  = 10000 // must match xds.HTTPListenPort
	edgeContainerPortS = "10000"
)

// EdgeSpec describes an edge Envoy proxy to run on a host.
type EdgeSpec struct {
	NodeID        string // Envoy --service-node; must match the xDS snapshot node
	BootstrapYAML string // full Envoy bootstrap (points at the central xDS)
	HostPort      int    // host port to publish the HTTP listener (10000) on
}

// EdgeStatus reports whether a host's edge Envoy is present/running.
type EdgeStatus struct {
	Present  bool   `json:"present"`
	Running  bool   `json:"running"`
	HostPort int    `json:"hostPort"`
	Image    string `json:"image"`
}

// DeployEdge (re)creates the edge Envoy on this host: it ensures the shared
// edge network exists, replaces any existing edge container, injects the
// bootstrap config, and starts Envoy pointed at the central xDS control plane.
func (c *Client) DeployEdge(ctx context.Context, spec EdgeSpec) error {
	if spec.NodeID == "" || spec.BootstrapYAML == "" {
		return fmt.Errorf("edge spec requires node id and bootstrap")
	}
	if spec.HostPort <= 0 {
		spec.HostPort = 80
	}
	if err := c.ensureEdgeNetwork(ctx); err != nil {
		return err
	}
	// Replace any existing edge proxy so config changes take effect.
	_ = c.cli.ContainerRemove(ctx, EdgeContainerName, container.RemoveOptions{Force: true})

	// Pull the Envoy image if it isn't present locally.
	if _, _, err := c.cli.ImageInspectWithRaw(ctx, edgeEnvoyImage); err != nil {
		if rc, perr := c.PullImage(ctx, edgeEnvoyImage); perr == nil {
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		}
	}

	port, err := nat.NewPort("tcp", edgeContainerPortS)
	if err != nil {
		return err
	}
	cfg := &container.Config{
		Image: edgeEnvoyImage,
		Cmd: []string{
			"-c", edgeBootstrapPath,
			"--service-node", spec.NodeID,
			"--service-cluster", "easydeploy",
		},
		ExposedPorts: nat.PortSet{port: struct{}{}},
		Labels:       map[string]string{LabelManaged: "true", LabelEdge: "true"},
	}
	hostCfg := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		PortBindings: nat.PortMap{port: []nat.PortBinding{
			{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", spec.HostPort)},
		}},
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{EdgeNetwork: {}},
	}
	created, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, EdgeContainerName)
	if err != nil {
		return fmt.Errorf("create edge proxy: %w", err)
	}
	// Inject the bootstrap file, then start. Envoy reads it via `-c`.
	if err := c.copyEdgeBootstrap(ctx, created.ID, spec.BootstrapYAML); err != nil {
		return fmt.Errorf("write edge bootstrap: %w", err)
	}
	if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start edge proxy: %w", err)
	}
	return nil
}

// EdgeStatus returns the state of this host's edge Envoy.
func (c *Client) EdgeStatus(ctx context.Context) (EdgeStatus, error) {
	info, err := c.cli.ContainerInspect(ctx, EdgeContainerName)
	if err != nil {
		if client.IsErrNotFound(err) {
			return EdgeStatus{Present: false}, nil
		}
		return EdgeStatus{}, err
	}
	st := EdgeStatus{Present: true, Image: edgeEnvoyImage}
	if info.State != nil {
		st.Running = info.State.Running
	}
	if info.HostConfig != nil {
		if binds := info.HostConfig.PortBindings[nat.Port(edgeContainerPortS+"/tcp")]; len(binds) > 0 {
			fmt.Sscanf(binds[0].HostPort, "%d", &st.HostPort)
		}
	}
	return st, nil
}

// RemoveEdge tears down this host's edge Envoy.
func (c *Client) RemoveEdge(ctx context.Context) error {
	err := c.cli.ContainerRemove(ctx, EdgeContainerName, container.RemoveOptions{Force: true})
	if client.IsErrNotFound(err) {
		return nil
	}
	return err
}

// ensureEdgeNetwork creates the shared edge network if it is missing.
func (c *Client) ensureEdgeNetwork(ctx context.Context) error {
	if _, err := c.cli.NetworkInspect(ctx, EdgeNetwork, network.InspectOptions{}); err == nil {
		return nil
	}
	if _, err := c.CreateNetwork(ctx, EdgeNetwork, "bridge"); err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create edge network: %w", err)
	}
	return nil
}

func (c *Client) copyEdgeBootstrap(ctx context.Context, id, yaml string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	name := edgeBootstrapPath[1:] // strip leading "/"
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(yaml))}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(yaml)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return c.cli.CopyToContainer(ctx, id, "/", &buf, container.CopyToContainerOptions{})
}
