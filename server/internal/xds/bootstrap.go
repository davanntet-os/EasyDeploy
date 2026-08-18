package xds

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// HTTPListenPort is the port an Envoy (local or edge) accepts public HTTP
// traffic on. Subdomains are demultiplexed by Host header. Exported so the
// edge-proxy deployer can publish it on the remote host.
const HTTPListenPort = httpListenPort

// AdminPort is the Envoy admin interface port in the generated edge bootstrap.
const AdminPort = 9901

// EdgeBootstrapYAML builds a full Envoy bootstrap for a remote edge proxy.
// Unlike the local deploy/envoy.yaml (which reaches the control plane via
// host.docker.internal), the edge proxy dials a concrete advertised address —
// this machine's LAN IP:port — because from the remote host
// host.docker.internal points at the wrong machine.
//
// nodeID must equal Manager.NodeForEndpoint(id); xdsAddr is host:port.
func EdgeBootstrapYAML(nodeID, xdsAddr string) (string, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(xdsAddr))
	if err != nil {
		return "", fmt.Errorf("xds advertise address must be host:port: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return "", fmt.Errorf("invalid xds advertise port %q", portStr)
	}
	if host == "" {
		return "", fmt.Errorf("xds advertise address must include a host")
	}
	// The node id is embedded here AND passed as --service-node; either alone is
	// enough, but keeping both explicit matches the local bootstrap and avoids a
	// silent empty-config (404) if one is dropped.
	return fmt.Sprintf(`node:
  id: %s
  cluster: easydeploy
admin:
  address:
    socket_address: { address: 0.0.0.0, port_value: %d }
dynamic_resources:
  cds_config:
    resource_api_version: V3
    api_config_source:
      api_type: GRPC
      transport_api_version: V3
      grpc_services:
        - envoy_grpc: { cluster_name: easydeploy_xds }
  lds_config:
    resource_api_version: V3
    api_config_source:
      api_type: GRPC
      transport_api_version: V3
      grpc_services:
        - envoy_grpc: { cluster_name: easydeploy_xds }
static_resources:
  clusters:
    - name: easydeploy_xds
      type: STRICT_DNS
      dns_lookup_family: V4_PREFERRED
      connect_timeout: 5s
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config:
            http2_protocol_options: {}
      load_assignment:
        cluster_name: easydeploy_xds
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address:
                      address: %s
                      port_value: %d
`, nodeID, AdminPort, host, port), nil
}
