// Package store provides PostgreSQL-backed persistence for EasyDeploy's
// declarative state: deployments, proxy routes, tunnel configs, and registry
// credentials. The Docker daemon remains the source of truth for live
// container state; the store only holds what EasyDeploy itself must remember
// across restarts.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Route maps a subdomain to a container target for the Envoy control plane.
type Route struct {
	ID          int64     `json:"id"`
	Subdomain   string    `json:"subdomain"`
	ContainerID string    `json:"containerId"`
	TargetHost  string    `json:"targetHost"`
	TargetPort  int       `json:"targetPort"`
	// EndpointID is the environment (Docker host) this route is served on
	// (0 = local). It selects which Envoy node the route is published to.
	EndpointID int64     `json:"endpointId"`
	CreatedAt  time.Time `json:"createdAt"`
}

// TunnelKind enumerates how a public IP is obtained.
type TunnelKind string

const (
	// TunnelWiFi exposes the service via the local network's NAT public IP.
	TunnelWiFi TunnelKind = "wifi"
	// TunnelSSH forwards traffic through a cloud VM via an SSH reverse tunnel.
	TunnelSSH TunnelKind = "ssh"
)

// Tunnel is a stored public-exposure configuration.
type Tunnel struct {
	ID         int64      `json:"id"`
	Kind       TunnelKind `json:"kind"`
	Name       string     `json:"name"`
	SSHHost    string     `json:"sshHost"`
	SSHPort    int        `json:"sshPort"`
	SSHUser    string     `json:"sshUser"`
	RemotePort int        `json:"remotePort"`
	LocalPort  int        `json:"localPort"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// Registry is a container image registry EasyDeploy can authenticate to. The
// password is stored encrypted (PasswordEnc); the registry service handles
// the plaintext <-> ciphertext mapping.
type Registry struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"` // host, e.g. "ghcr.io", "registry-1.docker.io"
	Username    string    `json:"username"`
	PasswordEnc string    `json:"-"` // never serialized to clients
	CreatedAt   time.Time `json:"createdAt"`
}

// Store wraps the pgx connection pool and typed accessors.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to PostgreSQL and runs migrations.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS routes (
    id           BIGSERIAL PRIMARY KEY,
    subdomain    TEXT NOT NULL UNIQUE,
    container_id TEXT NOT NULL,
    target_host  TEXT NOT NULL,
    target_port  INTEGER NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS tunnels (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL,
    name        TEXT NOT NULL,
    ssh_host    TEXT NOT NULL DEFAULT '',
    ssh_port    INTEGER NOT NULL DEFAULT 22,
    ssh_user    TEXT NOT NULL DEFAULT '',
    remote_port INTEGER NOT NULL DEFAULT 0,
    local_port  INTEGER NOT NULL DEFAULT 0,
    enabled     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS endpoints (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    host         TEXT NOT NULL,
    tls_ca_enc   TEXT NOT NULL DEFAULT '',
    tls_cert_enc TEXT NOT NULL DEFAULT '',
    tls_key_enc  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS registries (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    url          TEXT NOT NULL,
    username     TEXT NOT NULL DEFAULT '',
    password_enc TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS users (
    id              BIGSERIAL PRIMARY KEY,
    username        TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'member',
    cpu_quota_milli INTEGER NOT NULL DEFAULT 0,
    mem_quota_mb    INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS resource_requests (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username    TEXT NOT NULL,
    cpu_milli   INTEGER NOT NULL,
    mem_mb      INTEGER NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending',
    reviewed_by TEXT NOT NULL DEFAULT '',
    review_note TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS services (
    id                 BIGSERIAL PRIMARY KEY,
    name               TEXT NOT NULL UNIQUE,
    owner              TEXT NOT NULL DEFAULT '',
    image              TEXT NOT NULL,
    subdomain          TEXT NOT NULL DEFAULT '',
    container_port     INTEGER NOT NULL DEFAULT 0,
    network            TEXT NOT NULL DEFAULT '',
    env                TEXT NOT NULL DEFAULT '[]',
    cpu_milli          INTEGER NOT NULL DEFAULT 0,
    mem_mb             INTEGER NOT NULL DEFAULT 0,
    replicas           INTEGER NOT NULL DEFAULT 1,
    min_replicas       INTEGER NOT NULL DEFAULT 1,
    max_replicas       INTEGER NOT NULL DEFAULT 1,
    autoscale          BOOLEAN NOT NULL DEFAULT false,
    target_cpu_percent INTEGER NOT NULL DEFAULT 70,
    git_repo           TEXT NOT NULL DEFAULT '',
    git_branch         TEXT NOT NULL DEFAULT 'main',
    git_dockerfile     TEXT NOT NULL DEFAULT 'Dockerfile',
    webhook_token      TEXT NOT NULL DEFAULT '',
    last_image         TEXT NOT NULL DEFAULT '',
    last_deploy_at     TIMESTAMPTZ,
    advanced           TEXT NOT NULL DEFAULT '{}',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Additive migration for databases created before the advanced column existed.
ALTER TABLE services ADD COLUMN IF NOT EXISTS advanced TEXT NOT NULL DEFAULT '{}';
-- Multi-environment routing: which Docker host (endpoint) a route/service is
-- served on. 0 = local. Additive so existing rows default to the local host.
ALTER TABLE routes    ADD COLUMN IF NOT EXISTS endpoint_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE services  ADD COLUMN IF NOT EXISTS endpoint_id BIGINT NOT NULL DEFAULT 0;
-- Per-user environment grants: which remote environments a member may use
-- (admins may use all). The local host (id 0) is always allowed and not stored.
-- Each grant carries a per-environment quota (0 = none granted yet).
CREATE TABLE IF NOT EXISTS user_endpoints (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint_id     BIGINT NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    cpu_quota_milli INTEGER NOT NULL DEFAULT 0,
    mem_quota_mb    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, endpoint_id)
);
ALTER TABLE user_endpoints ADD COLUMN IF NOT EXISTS cpu_quota_milli INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_endpoints ADD COLUMN IF NOT EXISTS mem_quota_mb    INTEGER NOT NULL DEFAULT 0;
-- Resource requests target a specific environment (0 = local).
ALTER TABLE resource_requests ADD COLUMN IF NOT EXISTS endpoint_id BIGINT NOT NULL DEFAULT 0;`
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// --- Routes ---

// ListRoutes returns all persisted routes ordered by subdomain.
func (s *Store) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, subdomain, container_id, target_host, target_port, endpoint_id, created_at FROM routes ORDER BY subdomain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Route
	for rows.Next() {
		var r Route
		if err := rows.Scan(&r.ID, &r.Subdomain, &r.ContainerID, &r.TargetHost, &r.TargetPort, &r.EndpointID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertRoute inserts or updates a route keyed by subdomain.
func (s *Store) UpsertRoute(ctx context.Context, r Route) (Route, error) {
	err := s.pool.QueryRow(ctx, `
INSERT INTO routes (subdomain, container_id, target_host, target_port, endpoint_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (subdomain) DO UPDATE SET
    container_id = EXCLUDED.container_id,
    target_host  = EXCLUDED.target_host,
    target_port  = EXCLUDED.target_port,
    endpoint_id  = EXCLUDED.endpoint_id
RETURNING id, created_at`,
		r.Subdomain, r.ContainerID, r.TargetHost, r.TargetPort, r.EndpointID).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return Route{}, err
	}
	return r, nil
}

// DeleteRoute removes a route by subdomain.
func (s *Store) DeleteRoute(ctx context.Context, subdomain string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM routes WHERE subdomain = $1`, subdomain)
	return err
}

// --- Tunnels ---

// ListTunnels returns all tunnel configs.
func (s *Store) ListTunnels(ctx context.Context) ([]Tunnel, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, name, ssh_host, ssh_port, ssh_user, remote_port, local_port, enabled, created_at FROM tunnels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tunnel
	for rows.Next() {
		var t Tunnel
		if err := rows.Scan(&t.ID, &t.Kind, &t.Name, &t.SSHHost, &t.SSHPort, &t.SSHUser, &t.RemotePort, &t.LocalPort, &t.Enabled, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// InsertTunnel stores a new tunnel config.
func (s *Store) InsertTunnel(ctx context.Context, t Tunnel) (Tunnel, error) {
	err := s.pool.QueryRow(ctx, `
INSERT INTO tunnels (kind, name, ssh_host, ssh_port, ssh_user, remote_port, local_port, enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, created_at`,
		t.Kind, t.Name, t.SSHHost, t.SSHPort, t.SSHUser, t.RemotePort, t.LocalPort, t.Enabled).Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return Tunnel{}, err
	}
	return t, nil
}

// SetTunnelEnabled toggles a tunnel's enabled flag.
func (s *Store) SetTunnelEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE tunnels SET enabled = $1 WHERE id = $2`, enabled, id)
	return err
}

// DeleteTunnel removes a tunnel config.
func (s *Store) DeleteTunnel(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM tunnels WHERE id = $1`, id)
	return err
}

// --- Registries ---

// ListRegistries returns all registry configs (without decrypting passwords).
func (s *Store) ListRegistries(ctx context.Context) ([]Registry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, url, username, password_enc, created_at FROM registries ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registry
	for rows.Next() {
		var r Registry
		if err := rows.Scan(&r.ID, &r.Name, &r.URL, &r.Username, &r.PasswordEnc, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertRegistry stores a new registry config (PasswordEnc must already be
// encrypted).
func (s *Store) InsertRegistry(ctx context.Context, r Registry) (Registry, error) {
	err := s.pool.QueryRow(ctx, `
INSERT INTO registries (name, url, username, password_enc)
VALUES ($1, $2, $3, $4)
RETURNING id, created_at`,
		r.Name, r.URL, r.Username, r.PasswordEnc).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return Registry{}, err
	}
	return r, nil
}

// DeleteRegistry removes a registry config.
func (s *Store) DeleteRegistry(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM registries WHERE id = $1`, id)
	return err
}
