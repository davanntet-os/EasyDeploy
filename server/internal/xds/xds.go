// Package xds implements an Envoy xDS control plane. EasyDeploy runs Envoy
// as a data-plane proxy configured to fetch all of its dynamic
// configuration (listeners, routes, clusters, endpoints) from this control
// plane over gRPC. When a container is deployed or a route changes, the
// registry pushes a fresh snapshot and Envoy reconfigures live, with no
// restart.
package xds

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	routerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	clusterservice "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	endpointservice "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	listenerservice "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	routeservice "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	listenerName    = "easydeploy-listener"
	routeConfigName = "easydeploy-routes"
	// httpListenPort is the port Envoy accepts HTTP traffic on. All public
	// subdomains are served here and demultiplexed by Host header.
	httpListenPort = 10000
)

// Upstream is a routing target: one virtual host (matching one or more Host
// headers) load-balanced across a set of replica hosts. Multiple hosts produce
// a round-robin cluster (the load balancer).
type Upstream struct {
	// Key uniquely identifies this upstream within a node; it names the Envoy
	// cluster and virtual host. Falls back to Subdomain when empty.
	Key string
	// Subdomain is the optional custom label (used as the fallback identity and,
	// when Domains is empty, to derive a single "<subdomain>.<base>" match).
	Subdomain string
	// Domains are the full Host values Envoy matches for this upstream (FQDNs),
	// e.g. "web.llm-server.example.com" plus an optional "myapp.example.com".
	// When set they take precedence over the subdomain-derived default.
	Domains []string
	Hosts   []string // replica container names/IPs reachable from Envoy
	Port    uint32
	// EndpointID is the EasyDeploy environment this upstream lives on (0 =
	// local). It selects which Envoy node the upstream is published to and is
	// otherwise ignored when building Envoy resources.
	EndpointID int64
}

// Manager owns the snapshot cache and translates a set of Upstreams into
// Envoy resources.
type Manager struct {
	cache      cachev3.SnapshotCache
	nodeID     string
	baseDomain string
	version    atomic.Int64
}

// DefaultNodeID returns the node id of the local Envoy (environment 0).
func (m *Manager) DefaultNodeID() string { return m.nodeID }

// NodeForEndpoint returns the Envoy node id for an EasyDeploy environment.
// Environment 0 (local) uses the configured default node id; each remote
// environment gets a stable per-id node so the control plane can serve it a
// dedicated snapshot.
func (m *Manager) NodeForEndpoint(id int64) string {
	if id == 0 {
		return m.nodeID
	}
	return fmt.Sprintf("easydeploy-edge-%d", id)
}

// NewManager creates a control-plane manager for the given Envoy node ID and
// wildcard base domain.
func NewManager(nodeID, baseDomain string) *Manager {
	m := &Manager{
		cache:      cachev3.NewSnapshotCache(false, cachev3.IDHash{}, nil),
		nodeID:     nodeID,
		baseDomain: baseDomain,
	}
	// Seed the version from a monotonic wall-clock value so snapshot versions
	// always increase across server restarts. A long-running Envoy remembers
	// the last version it ACKed; if we restarted from 0 it would treat new
	// snapshots as stale and silently ignore them.
	m.version.Store(time.Now().UnixMilli())
	return m
}

// Register wires the control-plane services onto a gRPC server.
func (m *Manager) Register(gs *grpc.Server) {
	srv := serverv3.NewServer(context.Background(), m.cache, nil)
	discoverygrpc.RegisterAggregatedDiscoveryServiceServer(gs, srv)
	endpointservice.RegisterEndpointDiscoveryServiceServer(gs, srv)
	clusterservice.RegisterClusterDiscoveryServiceServer(gs, srv)
	routeservice.RegisterRouteDiscoveryServiceServer(gs, srv)
	listenerservice.RegisterListenerDiscoveryServiceServer(gs, srv)
}

// Update rebuilds the snapshot for one Envoy node from the given upstreams and
// publishes it. Each EasyDeploy environment has its own Envoy node (the local
// host plus one per remote edge proxy), so the control plane serves a distinct
// snapshot per node. It is safe to call concurrently with route changes.
func (m *Manager) Update(ctx context.Context, nodeID string, ups []Upstream) error {
	clusters := make([]types.Resource, 0, len(ups))
	for _, u := range ups {
		clusters = append(clusters, makeCluster(u))
	}
	resources := map[resourcev3.Type][]types.Resource{
		resourcev3.ClusterType:  clusters,
		resourcev3.RouteType:    {makeRouteConfig(ups, m.baseDomain)},
		resourcev3.ListenerType: {makeHTTPListener()},
	}
	version := strconv.FormatInt(m.version.Add(1), 10)
	snap, err := cachev3.NewSnapshot(version, resources)
	if err != nil {
		return fmt.Errorf("build snapshot: %w", err)
	}
	if err := snap.Consistent(); err != nil {
		return fmt.Errorf("inconsistent snapshot: %w", err)
	}
	return m.cache.SetSnapshot(ctx, nodeID, snap)
}

func upstreamKey(u Upstream) string {
	if u.Key != "" {
		return u.Key
	}
	return u.Subdomain
}

func clusterName(u Upstream) string { return "cl_" + upstreamKey(u) }

// matchDomains returns the Host values Envoy should match for this upstream,
// each paired with its ":*" (explicit-port) variant. Falls back to the
// subdomain-derived default when no explicit domains are set.
func matchDomains(u Upstream, baseDomain string) []string {
	src := u.Domains
	if len(src) == 0 && u.Subdomain != "" {
		src = []string{u.Subdomain + "." + baseDomain}
	}
	out := make([]string, 0, len(src)*2)
	for _, d := range src {
		out = append(out, d, d+":*")
	}
	return out
}

func makeCluster(u Upstream) *clusterv3.Cluster {
	lbEndpoints := make([]*endpointv3.LbEndpoint, 0, len(u.Hosts))
	for _, host := range u.Hosts {
		lbEndpoints = append(lbEndpoints, &endpointv3.LbEndpoint{
			HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
				Endpoint: &endpointv3.Endpoint{Address: socketAddress(host, u.Port)},
			},
		})
	}
	return &clusterv3.Cluster{
		Name:                 clusterName(u),
		ConnectTimeout:       durationpb.New(5 * time.Second),
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STRICT_DNS},
		LbPolicy:             clusterv3.Cluster_ROUND_ROBIN,
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: clusterName(u),
			Endpoints: []*endpointv3.LocalityLbEndpoints{{
				LbEndpoints: lbEndpoints,
			}},
		},
	}
}

func makeRouteConfig(ups []Upstream, baseDomain string) *routev3.RouteConfiguration {
	vhosts := make([]*routev3.VirtualHost, 0, len(ups)+1)
	// Envoy rejects a route config where two virtual hosts claim the same domain,
	// so dedupe: the first upstream to claim a Host wins.
	claimed := make(map[string]bool)
	for _, u := range ups {
		domains := make([]string, 0, 2)
		for _, d := range matchDomains(u, baseDomain) {
			if !claimed[d] {
				claimed[d] = true
				domains = append(domains, d)
			}
		}
		if len(domains) == 0 {
			continue // nothing to match (or all domains already claimed)
		}
		vhosts = append(vhosts, &routev3.VirtualHost{
			Name:    "vh_" + upstreamKey(u),
			Domains: domains,
			Routes: []*routev3.Route{{
				Match: &routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"}},
				Action: &routev3.Route_Route{Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: clusterName(u)},
				}},
			}},
		})
	}
	// Envoy requires at least one virtual host. A catch-all returns 404 for
	// hosts that don't match any known subdomain.
	vhosts = append(vhosts, &routev3.VirtualHost{
		Name:    "vh_default",
		Domains: []string{"*"},
		Routes: []*routev3.Route{{
			Match: &routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"}},
			Action: &routev3.Route_DirectResponse{DirectResponse: &routev3.DirectResponseAction{
				Status: 404,
			}},
		}},
	})
	return &routev3.RouteConfiguration{Name: routeConfigName, VirtualHosts: vhosts}
}

func makeHTTPListener() *listenerv3.Listener {
	routerAny, _ := anypb.New(&routerv3.Router{})
	manager := &hcm.HttpConnectionManager{
		CodecType:  hcm.HttpConnectionManager_AUTO,
		StatPrefix: "easydeploy_http",
		RouteSpecifier: &hcm.HttpConnectionManager_Rds{
			Rds: &hcm.Rds{
				ConfigSource:    configSource(),
				RouteConfigName: routeConfigName,
			},
		},
		HttpFilters: []*hcm.HttpFilter{{
			Name:       "envoy.filters.http.router",
			ConfigType: &hcm.HttpFilter_TypedConfig{TypedConfig: routerAny},
		}},
	}
	mgrAny, _ := anypb.New(manager)
	return &listenerv3.Listener{
		Name:    listenerName,
		Address: socketAddress("0.0.0.0", httpListenPort),
		FilterChains: []*listenerv3.FilterChain{{
			Filters: []*listenerv3.Filter{{
				Name:       "envoy.filters.network.http_connection_manager",
				ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: mgrAny},
			}},
		}},
	}
}

// configSource points RDS at the same xDS gRPC cluster defined in Envoy's
// bootstrap config (deploy/envoy.yaml).
func configSource() *corev3.ConfigSource {
	return &corev3.ConfigSource{
		ResourceApiVersion: corev3.ApiVersion_V3,
		ConfigSourceSpecifier: &corev3.ConfigSource_ApiConfigSource{
			ApiConfigSource: &corev3.ApiConfigSource{
				TransportApiVersion:       corev3.ApiVersion_V3,
				ApiType:                   corev3.ApiConfigSource_GRPC,
				SetNodeOnFirstMessageOnly: true,
				GrpcServices: []*corev3.GrpcService{{
					TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
						EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{ClusterName: "easydeploy_xds"},
					},
				}},
			},
		},
	}
}

func socketAddress(addr string, port uint32) *corev3.Address {
	return &corev3.Address{
		Address: &corev3.Address_SocketAddress{
			SocketAddress: &corev3.SocketAddress{
				Protocol:      corev3.SocketAddress_TCP,
				Address:       addr,
				PortSpecifier: &corev3.SocketAddress_PortValue{PortValue: port},
			},
		},
	}
}
