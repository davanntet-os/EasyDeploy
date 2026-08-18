// Package config loads EasyDeploy runtime configuration from environment
// variables, applying sensible defaults for local development.
package config

import (
	"os"
	"strconv"
)

// Config is the fully-resolved runtime configuration for the server.
type Config struct {
	// HTTPAddr is the listen address for the REST/WebSocket API and the
	// embedded web UI.
	HTTPAddr string
	// XDSAddr is the listen address for the Envoy xDS gRPC control plane.
	XDSAddr string
	// DatabaseURL is the PostgreSQL connection string (postgres://...).
	DatabaseURL string
	// AdminPassword is the single-admin login password. Required.
	AdminPassword string
	// JWTSecret signs session tokens. Generated per-process if unset (which
	// invalidates sessions on restart).
	JWTSecret string
	// SecretKey is the master key used to derive the AES-GCM key that
	// encrypts registry credentials at rest. Required.
	SecretKey string
	// DockerHost overrides the Docker daemon endpoint. Empty means the
	// Docker SDK resolves it from the environment (DOCKER_HOST) or the
	// default unix socket.
	DockerHost string
	// BaseDomain is the wildcard domain under which container subdomains are
	// published, e.g. "apps.example.com" -> "<name>.apps.example.com".
	BaseDomain string
	// EnvoyNodeID must match the --service-node passed to the Envoy process
	// so snapshots are delivered to the right node.
	EnvoyNodeID string
	// XDSAdvertiseAddr is the host:port a *remote* edge Envoy uses to dial back
	// to this control plane's xDS gRPC server. It must be an address reachable
	// from the remote host (e.g. this machine's LAN IP + the xDS port). Empty
	// disables deploying edge proxies on remote environments — from a remote
	// host "host.docker.internal" resolves to the wrong machine, so a concrete
	// advertised address is required.
	XDSAdvertiseAddr string
	// PublicIPService is queried to discover the current WiFi/NAT public IP.
	PublicIPService string
	// WebDir, if set, is a directory of built frontend assets served by the
	// server as a single-page app. Empty disables static serving (use the
	// Vite dev server instead).
	WebDir string
}

// Load reads configuration from the environment and fills in defaults.
func Load() Config {
	return Config{
		HTTPAddr:        env("EASYDEPLOY_HTTP_ADDR", ":8080"),
		XDSAddr:         env("EASYDEPLOY_XDS_ADDR", ":18000"),
		DatabaseURL:     env("EASYDEPLOY_DATABASE_URL", "postgres://easydeploy:easydeploy@localhost:5432/easydeploy?sslmode=disable"),
		AdminPassword:   env("EASYDEPLOY_ADMIN_PASSWORD", ""),
		JWTSecret:       env("EASYDEPLOY_JWT_SECRET", ""),
		SecretKey:       env("EASYDEPLOY_SECRET_KEY", ""),
		DockerHost:      env("DOCKER_HOST", ""),
		BaseDomain:      env("EASYDEPLOY_BASE_DOMAIN", "localhost"),
		EnvoyNodeID:      env("EASYDEPLOY_ENVOY_NODE_ID", "easydeploy-envoy"),
		XDSAdvertiseAddr: env("EASYDEPLOY_XDS_ADVERTISE_ADDR", ""),
		PublicIPService: env("EASYDEPLOY_PUBLIC_IP_SERVICE", "https://api.ipify.org"),
		WebDir:          env("EASYDEPLOY_WEB_DIR", ""),
	}
}

// Validate ensures required security-sensitive settings are present.
func (c Config) Validate() error {
	if c.AdminPassword == "" {
		return errRequired("EASYDEPLOY_ADMIN_PASSWORD")
	}
	if c.SecretKey == "" {
		return errRequired("EASYDEPLOY_SECRET_KEY")
	}
	return nil
}

func errRequired(name string) error {
	return &RequiredError{Name: name}
}

// RequiredError indicates a mandatory configuration value was not provided.
type RequiredError struct{ Name string }

func (e *RequiredError) Error() string {
	return e.Name + " is required but not set"
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// envInt is kept for future numeric settings (timeouts, ports).
func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

var _ = envInt
