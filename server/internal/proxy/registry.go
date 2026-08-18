// Package proxy is the source of truth for subdomain routing. It persists
// routes in the store and, on every change, recomputes the full set of
// upstreams and pushes a fresh snapshot to the Envoy control plane.
package proxy

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"easydeploy/internal/store"
	"easydeploy/internal/xds"
)

// Snapshotter is the subset of the xDS manager the registry depends on.
type Snapshotter interface {
	// Update publishes one Envoy node's full upstream set.
	Update(ctx context.Context, nodeID string, ups []xds.Upstream) error
	// NodeForEndpoint maps an EasyDeploy environment id to its Envoy node id.
	NodeForEndpoint(id int64) string
}

// ServiceUpstream is a load-balanced service contributed by the service
// manager. The registry turns it into the actual match domains (the auto host
// plus any custom subdomain).
type ServiceUpstream struct {
	Service    string   // service name (drives the auto host <name>.<server>.<base>)
	Subdomain  string   // optional custom label -> <subdomain>.<base>
	Hosts      []string // replica container names
	Port       uint32
	EndpointID int64
}

// ServiceSource contributes load-balanced, multi-replica upstreams (from the
// service manager). It is optional; when set, its upstreams are merged with
// the persisted manual routes and win on custom-subdomain conflicts.
type ServiceSource interface {
	Upstreams(ctx context.Context) ([]ServiceUpstream, error)
}

// Registry manages the mapping of subdomains to container targets.
type Registry struct {
	store      *store.Store
	xds        Snapshotter
	baseDomain string
	svcSrc     ServiceSource
	mu         sync.Mutex
}

// New creates a Registry backed by the given store and control plane. baseDomain
// is the wildcard domain under which hosts are published.
func New(st *store.Store, x Snapshotter, baseDomain string) *Registry {
	if baseDomain == "" {
		baseDomain = "localhost"
	}
	return &Registry{store: st, xds: x, baseDomain: baseDomain}
}

// SetServiceSource attaches the service manager as an upstream contributor.
func (r *Registry) SetServiceSource(src ServiceSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.svcSrc = src
}

// Sync loads all persisted routes and republishes them to Envoy. Call once
// on startup so the proxy state survives restarts.
func (r *Registry) Sync(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.publish(ctx)
}

// List returns all current routes.
func (r *Registry) List(ctx context.Context) ([]store.Route, error) {
	return r.store.ListRoutes(ctx)
}

// ServiceDomains returns the Host values a service is reachable at: the auto
// host "<name>.<server>.<base>" (always) plus an optional custom
// "<subdomain>.<base>". Used by the API to show a service's addresses.
func (r *Registry) ServiceDomains(ctx context.Context, service, subdomain string, endpointID int64) []string {
	slugName := "local"
	if endpointID != 0 {
		if ep, err := r.store.GetEndpoint(ctx, endpointID); err == nil {
			slugName = slug(ep.Name)
		}
	}
	domains := []string{service + "." + slugName + "." + r.baseDomain}
	if subdomain != "" {
		domains = append(domains, subdomain+"."+r.baseDomain)
	}
	return domains
}

// Upsert creates or replaces the route for a subdomain and repushes the
// snapshot.
func (r *Registry) Upsert(ctx context.Context, route store.Route) (store.Route, error) {
	if route.Subdomain == "" {
		return store.Route{}, fmt.Errorf("subdomain is required")
	}
	if route.TargetPort <= 0 {
		return store.Route{}, fmt.Errorf("target port must be positive")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	saved, err := r.store.UpsertRoute(ctx, route)
	if err != nil {
		return store.Route{}, err
	}
	if err := r.publish(ctx); err != nil {
		return store.Route{}, err
	}
	return saved, nil
}

// Delete removes a route and repushes the snapshot.
func (r *Registry) Delete(ctx context.Context, subdomain string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.store.DeleteRoute(ctx, subdomain); err != nil {
		return err
	}
	return r.publish(ctx)
}

// publish reads the full route set plus any service upstreams, resolves each to
// its match domains, groups them by environment, and pushes one snapshot per
// Envoy node — the local Envoy plus one per remote edge proxy. Services win over
// a manual route that claims the same custom subdomain. Callers must hold r.mu.
func (r *Registry) publish(ctx context.Context) error {
	eps, err := r.store.ListEndpoints(ctx)
	if err != nil {
		return err
	}
	// Per-environment server slug for the auto host <name>.<server>.<base>.
	serverSlug := map[int64]string{0: "local"}
	for _, e := range eps {
		serverSlug[e.ID] = slug(e.Name)
	}

	var ups []xds.Upstream
	svcSubs := map[string]bool{} // custom subdomains claimed by services

	if r.svcSrc != nil {
		svcUps, err := r.svcSrc.Upstreams(ctx)
		if err != nil {
			return err
		}
		for _, u := range svcUps {
			if len(u.Hosts) == 0 {
				continue
			}
			slugName := serverSlug[u.EndpointID]
			if slugName == "" {
				slugName = "local"
			}
			// Auto host is always present; a custom subdomain adds a second host.
			domains := []string{u.Service + "." + slugName + "." + r.baseDomain}
			if u.Subdomain != "" {
				domains = append(domains, u.Subdomain+"."+r.baseDomain)
				svcSubs[u.Subdomain] = true
			}
			ups = append(ups, xds.Upstream{
				Key:        "svc-" + slugName + "-" + u.Service,
				Subdomain:  u.Subdomain,
				Domains:    domains,
				Hosts:      u.Hosts,
				Port:       u.Port,
				EndpointID: u.EndpointID,
			})
		}
	}

	routes, err := r.store.ListRoutes(ctx)
	if err != nil {
		return err
	}
	for _, rt := range routes {
		if svcSubs[rt.Subdomain] {
			continue // a service owns this subdomain
		}
		ups = append(ups, xds.Upstream{
			Key:        "rt-" + rt.Subdomain,
			Subdomain:  rt.Subdomain,
			Domains:    []string{rt.Subdomain + "." + r.baseDomain},
			Hosts:      []string{rt.TargetHost},
			Port:       uint32(rt.TargetPort),
			EndpointID: rt.EndpointID,
		})
	}

	// Group upstreams by the environment (Envoy node) they belong to.
	byEnv := make(map[int64][]xds.Upstream)
	for _, u := range ups {
		byEnv[u.EndpointID] = append(byEnv[u.EndpointID], u)
	}
	// Publish to the local node plus every known remote environment, even those
	// with no upstreams — an empty snapshot clears a node whose last route was
	// just deleted. A remote node with no connected edge Envoy simply keeps its
	// snapshot cached until one connects.
	nodes := map[int64]bool{0: true}
	for _, e := range eps {
		nodes[e.ID] = true
	}
	for id := range byEnv {
		nodes[id] = true
	}
	for id := range nodes {
		if err := r.xds.Update(ctx, r.xds.NodeForEndpoint(id), byEnv[id]); err != nil {
			return err
		}
	}
	return nil
}

// slug lowercases a name and replaces runs of non-alphanumeric characters with
// a single hyphen, so an environment name is safe in a hostname label.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "host"
	}
	return out
}
