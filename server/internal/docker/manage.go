package docker

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
)

// ListByLabel returns containers matching a "key=value" label selector.
func (c *Client) ListByLabel(ctx context.Context, label string, all bool) ([]types.Container, error) {
	f := filters.NewArgs(filters.Arg("label", label))
	return c.cli.ContainerList(ctx, container.ListOptions{All: all, Filters: f})
}

// OwnerUsage sums the CPU (nano-cores) and memory (bytes) limits across all
// containers owned by the given user. This is the live basis for enforcing a
// member's quota: the container labels + host-config limits are the source of
// truth, so the count stays correct even if the database drifts.
func (c *Client) OwnerUsage(ctx context.Context, owner string) (nanoCPUs int64, memBytes int64, err error) {
	return c.OwnerUsageExcludingService(ctx, owner, "")
}

// OwnerUsageExcludingService is like OwnerUsage but skips the replicas of one
// service. Used when editing a service so its own (about-to-be-replaced)
// replicas don't count against the owner's quota twice.
func (c *Client) OwnerUsageExcludingService(ctx context.Context, owner, excludeService string) (nanoCPUs int64, memBytes int64, err error) {
	f := filters.NewArgs(filters.Arg("label", LabelOwner+"="+owner))
	list, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return 0, 0, err
	}
	for _, summary := range list {
		info, err := c.cli.ContainerInspect(ctx, summary.ID)
		if err != nil {
			continue // container vanished mid-scan; skip it
		}
		if excludeService != "" && info.Config != nil && info.Config.Labels[LabelService] == excludeService {
			continue
		}
		if info.HostConfig != nil {
			nanoCPUs += info.HostConfig.NanoCPUs
			memBytes += info.HostConfig.Memory
		}
	}
	return nanoCPUs, memBytes, nil
}

// --- Networks ---

// CreateNetwork creates a user-defined bridge network (or the given driver),
// tagged with the given labels (e.g. easydeploy.owner), and returns its ID.
func (c *Client) CreateNetwork(ctx context.Context, name, driver string, labels map[string]string) (string, error) {
	if driver == "" {
		driver = "bridge"
	}
	resp, err := c.cli.NetworkCreate(ctx, name, network.CreateOptions{Driver: driver, Labels: labels})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// RemoveNetwork deletes a network by ID or name.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	return c.cli.NetworkRemove(ctx, id)
}

// InspectNetwork returns full details for a network.
func (c *Client) InspectNetwork(ctx context.Context, id string) (network.Inspect, error) {
	return c.cli.NetworkInspect(ctx, id, network.InspectOptions{})
}

// ConnectNetwork attaches a container to a network.
func (c *Client) ConnectNetwork(ctx context.Context, networkID, containerID string) error {
	return c.cli.NetworkConnect(ctx, networkID, containerID, nil)
}

// DisconnectNetwork detaches a container from a network.
func (c *Client) DisconnectNetwork(ctx context.Context, networkID, containerID string) error {
	return c.cli.NetworkDisconnect(ctx, networkID, containerID, false)
}

// EditContainer reconfigures a container by removing it and recreating it
// from the given spec under the same name. Unlike Recreate (which preserves
// the existing config and only swaps the image), this applies a fully new
// configuration: image, env, ports, network. Returns the new container ID.
func (c *Client) EditContainer(ctx context.Context, id string, spec DeploySpec) (string, error) {
	info, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", fmt.Errorf("inspect: %w", err)
	}
	name := strings.TrimPrefix(info.Name, "/")
	if spec.Name == "" {
		spec.Name = name
	}
	// Free the name before recreating.
	_ = c.cli.ContainerStop(ctx, id, container.StopOptions{})
	if err := c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		return "", fmt.Errorf("remove old: %w", err)
	}
	return c.Deploy(ctx, spec)
}

// --- Container update (recreate in place) ---

// Recreate updates a running container by pulling a (possibly new) image and
// recreating the container under the same name, preserving its configuration,
// host settings, and network attachments. If newImage is empty the current
// image reference is re-pulled (useful for rolling to a moved tag like
// :latest). Returns the new container ID.
//
// Docker containers are immutable: "updating" means replace. This is the
// standard recreate pattern used by Portainer/Watchtower.
func (c *Client) Recreate(ctx context.Context, id, newImage, auth string) (string, error) {
	info, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", fmt.Errorf("inspect: %w", err)
	}
	cfg := info.Config
	hostCfg := info.HostConfig
	if cfg == nil {
		return "", fmt.Errorf("container has no config")
	}
	if newImage != "" {
		cfg.Image = newImage
	}

	// Pull the target image (best-effort; a local image still works).
	if rc, perr := c.PullImageAuth(ctx, cfg.Image, auth); perr == nil {
		_, _ = io.Copy(io.Discard, rc)
		_ = rc.Close()
	}

	// Capture existing network attachments to reattach after recreate.
	var endpoints map[string]*network.EndpointSettings
	if info.NetworkSettings != nil {
		endpoints = info.NetworkSettings.Networks
	}

	name := strings.TrimPrefix(info.Name, "/")

	// Stop and remove the old container so the name is free.
	_ = c.cli.ContainerStop(ctx, id, container.StopOptions{})
	if err := c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		return "", fmt.Errorf("remove old: %w", err)
	}

	// Docker rejects create when more than one endpoint is specified inline;
	// attach the first here and connect the rest afterwards.
	var netCfg *network.NetworkingConfig
	var extraNetworks []string
	if len(endpoints) > 0 {
		netCfg = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
		first := true
		for netName, ep := range endpoints {
			if first {
				netCfg.EndpointsConfig[netName] = ep
				first = false
				continue
			}
			extraNetworks = append(extraNetworks, netName)
		}
	}

	created, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name)
	if err != nil {
		return "", fmt.Errorf("create new: %w", err)
	}
	for _, netName := range extraNetworks {
		_ = c.cli.NetworkConnect(ctx, netName, created.ID, endpoints[netName])
	}
	if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start new: %w", err)
	}
	return created.ID, nil
}
