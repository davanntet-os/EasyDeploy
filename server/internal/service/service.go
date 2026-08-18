// Package service manages replica-based applications: it reconciles the set
// of replica containers behind a service, feeds their addresses to the Envoy
// control plane (load balancing), autoscales on CPU, and rebuilds from git on
// webhook. A Service owns containers named "<service>-<index>".
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"easydeploy/internal/docker"
	"easydeploy/internal/endpoint"
	"easydeploy/internal/proxy"
	"easydeploy/internal/registry"
	"easydeploy/internal/store"
)

// Manager reconciles services and their replicas. A service runs on a specific
// environment (Docker host); the manager resolves the right docker client per
// service via the endpoint manager, so services work on remote hosts too.
type Manager struct {
	store      *store.Store
	endpoints  *endpoint.Manager
	registry   *proxy.Registry
	registries *registry.Service
	mu         sync.Mutex
}

// New creates a service manager.
func New(st *store.Store, eps *endpoint.Manager, reg *proxy.Registry, images *registry.Service) *Manager {
	return &Manager{store: st, endpoints: eps, registry: reg, registries: images}
}

// dockerFor resolves the docker client for the host a service runs on.
func (m *Manager) dockerFor(ctx context.Context, svc store.Service) (*docker.Client, error) {
	return m.endpoints.ClientFor(ctx, svc.EndpointID)
}

// Upstreams implements proxy.ServiceSource: it maps each service to the
// container names of its replicas so Envoy round-robins across them. Every
// service gets an auto host (built by the registry from its name + server); the
// custom subdomain is optional, so it is no longer required here.
func (m *Manager) Upstreams(ctx context.Context) ([]proxy.ServiceUpstream, error) {
	svcs, err := m.store.ListServices(ctx, "")
	if err != nil {
		return nil, err
	}
	var ups []proxy.ServiceUpstream
	for _, svc := range svcs {
		if svc.ContainerPort == 0 || svc.Replicas <= 0 {
			continue
		}
		// Use the desired replica names rather than currently-running
		// containers: this avoids a start-up race and lets Envoy's STRICT_DNS
		// resolve each replica as it comes up (unresolved names stay
		// unhealthy and receive no traffic).
		hosts := make([]string, 0, svc.Replicas)
		for i := 0; i < svc.Replicas; i++ {
			hosts = append(hosts, replicaName(svc.Name, i))
		}
		ups = append(ups, proxy.ServiceUpstream{
			Service:    svc.Name,
			Subdomain:  svc.Subdomain,
			Hosts:      hosts,
			Port:       uint32(svc.ContainerPort),
			EndpointID: svc.EndpointID,
		})
	}
	return ups, nil
}

// Create persists a service, launches its replicas, and publishes the route.
func (m *Manager) Create(ctx context.Context, svc store.Service) (store.Service, error) {
	saved, err := m.store.CreateService(ctx, svc)
	if err != nil {
		return store.Service{}, err
	}
	if err := m.reconcile(ctx, saved); err != nil {
		return store.Service{}, err
	}
	return saved, m.registry.Sync(ctx)
}

// List returns services (all, or a single owner's).
func (m *Manager) List(ctx context.Context, owner string) ([]store.Service, error) {
	return m.store.ListServices(ctx, owner)
}

// Get returns one service by name.
func (m *Manager) Get(ctx context.Context, name string) (store.Service, error) {
	return m.store.GetService(ctx, name)
}

// Update applies a new configuration to an existing service and rolls the
// change out to its replicas (one at a time). Identity fields (name, owner,
// webhook token, environment) are preserved from the stored service; the caller
// supplies the editable fields in `in`.
func (m *Manager) Update(ctx context.Context, name string, in store.Service) (store.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, err := m.store.GetService(ctx, name)
	if err != nil {
		return store.Service{}, err
	}
	// Preserve identity; take everything else from the request.
	in.ID = cur.ID
	in.Name = cur.Name
	in.Owner = cur.Owner
	in.WebhookToken = cur.WebhookToken
	in.EndpointID = cur.EndpointID
	if err := m.store.UpdateService(ctx, in); err != nil {
		return store.Service{}, err
	}
	cli, err := m.dockerFor(ctx, in)
	if err != nil {
		return store.Service{}, err
	}
	// Rolling replace: recreate each desired replica with the new spec.
	for i := 0; i < in.Replicas; i++ {
		rn := replicaName(name, i)
		_ = cli.Remove(ctx, rn) // ignore if absent
		spec, err := m.replicaSpec(ctx, in, i)
		if err != nil {
			return store.Service{}, err
		}
		if _, err := cli.Deploy(ctx, spec); err != nil {
			return store.Service{}, fmt.Errorf("update replica %d: %w", i, err)
		}
	}
	// Drop any leftover replicas if the count shrank.
	existing, err := cli.ListByLabel(ctx, docker.LabelService+"="+name, true)
	if err == nil {
		for _, c := range existing {
			if idx, err := strconv.Atoi(c.Labels[docker.LabelReplica]); err == nil && idx >= in.Replicas {
				_ = cli.Remove(ctx, c.ID)
			}
		}
	}
	return in, m.registry.Sync(ctx)
}

// SetSubdomain sets (or clears) a service's optional custom subdomain and
// republishes routing so the new host takes effect immediately.
func (m *Manager) SetSubdomain(ctx context.Context, name, subdomain string) error {
	svc, err := m.store.GetService(ctx, name)
	if err != nil {
		return err
	}
	if err := m.store.SetServiceSubdomain(ctx, svc.ID, subdomain); err != nil {
		return err
	}
	return m.registry.Sync(ctx)
}

// Scale changes the desired replica count and reconciles.
func (m *Manager) Scale(ctx context.Context, name string, replicas int) error {
	svc, err := m.store.GetService(ctx, name)
	if err != nil {
		return err
	}
	if replicas < 0 {
		replicas = 0
	}
	if err := m.store.SetServiceReplicas(ctx, svc.ID, replicas); err != nil {
		return err
	}
	svc.Replicas = replicas
	if err := m.reconcile(ctx, svc); err != nil {
		return err
	}
	return m.registry.Sync(ctx)
}

// Redeploy replaces every replica, optionally with a new image, one at a time
// (rolling). Passing an empty image re-pulls the current one.
func (m *Manager) Redeploy(ctx context.Context, name, newImage string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, err := m.store.GetService(ctx, name)
	if err != nil {
		return err
	}
	if newImage != "" {
		svc.Image = newImage
	}
	if err := m.store.SetServiceImage(ctx, svc.ID, svc.Image); err != nil {
		return err
	}
	cli, err := m.dockerFor(ctx, svc)
	if err != nil {
		return err
	}
	for i := 0; i < svc.Replicas; i++ {
		replicaName := replicaName(name, i)
		_ = cli.Remove(ctx, replicaName) // ignore if absent
		spec, err := m.replicaSpec(ctx, svc, i)
		if err != nil {
			return err
		}
		if _, err := cli.Deploy(ctx, spec); err != nil {
			return fmt.Errorf("redeploy replica %d: %w", i, err)
		}
	}
	return m.registry.Sync(ctx)
}

// Delete removes all replicas and the service record.
func (m *Manager) Delete(ctx context.Context, name string) error {
	svc, err := m.store.GetService(ctx, name)
	if err != nil {
		return err
	}
	cli, err := m.dockerFor(ctx, svc)
	if err != nil {
		return err
	}
	reps, err := cli.ListByLabel(ctx, docker.LabelService+"="+name, true)
	if err != nil {
		return err
	}
	for _, c := range reps {
		_ = cli.Remove(ctx, c.ID)
	}
	if err := m.store.DeleteService(ctx, svc.ID); err != nil {
		return err
	}
	return m.registry.Sync(ctx)
}

// SyncAll reconciles every service (used on startup).
func (m *Manager) SyncAll(ctx context.Context) error {
	svcs, err := m.store.ListServices(ctx, "")
	if err != nil {
		return err
	}
	for _, svc := range svcs {
		if err := m.reconcile(ctx, svc); err != nil {
			return err
		}
	}
	return m.registry.Sync(ctx)
}

// reconcile makes the running replica set match svc.Replicas, on the host the
// service is assigned to.
func (m *Manager) reconcile(ctx context.Context, svc store.Service) error {
	cli, err := m.dockerFor(ctx, svc)
	if err != nil {
		return err
	}
	existing, err := cli.ListByLabel(ctx, docker.LabelService+"="+svc.Name, true)
	if err != nil {
		return err
	}
	byIndex := make(map[int]string) // replica index -> container id
	for _, c := range existing {
		if idx, err := strconv.Atoi(c.Labels[docker.LabelReplica]); err == nil {
			byIndex[idx] = c.ID
			if c.State != "running" {
				_ = cli.Start(ctx, c.ID)
			}
		}
	}
	// Ensure replicas 0..N-1 exist.
	for i := 0; i < svc.Replicas; i++ {
		if _, ok := byIndex[i]; ok {
			delete(byIndex, i)
			continue
		}
		spec, err := m.replicaSpec(ctx, svc, i)
		if err != nil {
			return err
		}
		if _, err := cli.Deploy(ctx, spec); err != nil {
			return fmt.Errorf("start replica %d: %w", i, err)
		}
	}
	// Remove any leftover replicas (indices >= N).
	for _, id := range byIndex {
		_ = cli.Remove(ctx, id)
	}
	return nil
}

func (m *Manager) replicaSpec(ctx context.Context, svc store.Service, i int) (docker.DeploySpec, error) {
	var env []string
	if svc.Env != "" {
		_ = json.Unmarshal([]byte(svc.Env), &env)
	}
	var adv docker.AdvancedSpec
	if svc.Advanced != "" {
		_ = json.Unmarshal([]byte(svc.Advanced), &adv)
	}
	// A published host port can only bind one container, so every replica using
	// the same fixed host port would collide ("port is already allocated") and
	// the extra replicas would stay "created". Offset each replica's fixed host
	// ports by its index (base → replica 0, base+1 → replica 1, …) so all
	// replicas start and each is directly reachable; the subdomain still
	// load-balances across them via Envoy. Ephemeral ports (empty host port)
	// are left alone — Docker assigns a free port per replica.
	adv.Ports = offsetHostPorts(adv.Ports, i)
	auth, _ := m.registries.AuthForImage(ctx, svc.Image)
	return docker.DeploySpec{
		Name:          replicaName(svc.Name, i),
		Image:         svc.Image,
		Env:           env,
		Subdomain:     svc.Subdomain,
		ContainerPort: svc.ContainerPort,
		Network:       svc.Network,
		Owner:         svc.Owner,
		NanoCPUs:      int64(svc.CPUMilli) * 1_000_000,
		MemoryBytes:   int64(svc.MemMB) << 20,
		RegistryAuth:  auth,
		ExtraLabels: map[string]string{
			docker.LabelService: svc.Name,
			docker.LabelReplica: strconv.Itoa(i),
		},
		AdvancedSpec: adv,
	}, nil
}

func replicaName(service string, i int) string {
	return fmt.Sprintf("%s-%d", service, i)
}

// offsetHostPorts returns a copy of the port maps with each numeric, fixed host
// port shifted by delta, so replica i never collides with replica 0 on a
// published host port. Ephemeral ports (empty host port) and non-numeric values
// are passed through unchanged.
func offsetHostPorts(ports []docker.PortMap, delta int) []docker.PortMap {
	if len(ports) == 0 || delta == 0 {
		return ports
	}
	out := make([]docker.PortMap, len(ports))
	for j, p := range ports {
		out[j] = p
		hp := strings.TrimSpace(p.HostPort)
		if n, err := strconv.Atoi(hp); err == nil && hp != "" {
			out[j].HostPort = strconv.Itoa(n + delta)
		}
	}
	return out
}
