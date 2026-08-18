package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Endpoint is a remote Docker environment EasyDeploy can manage (in addition
// to the local host). Connection is via the Docker HTTP API; TLS client
// material is stored encrypted.
type Endpoint struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Host       string    `json:"host"` // e.g. "tcp://10.0.0.5:2376"
	TLSCAEnc   string    `json:"-"`
	TLSCertEnc string    `json:"-"`
	TLSKeyEnc  string    `json:"-"`
	TLS        bool      `json:"tls"` // whether TLS is configured
	CreatedAt  time.Time `json:"createdAt"`
}

const epCols = `id, name, host, tls_ca_enc, tls_cert_enc, tls_key_enc, created_at`

func scanEndpoint(row pgx.Row) (Endpoint, error) {
	var e Endpoint
	if err := row.Scan(&e.ID, &e.Name, &e.Host, &e.TLSCAEnc, &e.TLSCertEnc, &e.TLSKeyEnc, &e.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Endpoint{}, ErrNotFound
		}
		return Endpoint{}, err
	}
	e.TLS = e.TLSCertEnc != ""
	return e, nil
}

// CreateEndpoint stores a new remote endpoint (encrypted TLS material).
func (s *Store) CreateEndpoint(ctx context.Context, e Endpoint) (Endpoint, error) {
	err := s.pool.QueryRow(ctx, `
INSERT INTO endpoints (name, host, tls_ca_enc, tls_cert_enc, tls_key_enc)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, created_at`,
		e.Name, e.Host, e.TLSCAEnc, e.TLSCertEnc, e.TLSKeyEnc).Scan(&e.ID, &e.CreatedAt)
	if err != nil {
		return Endpoint{}, err
	}
	e.TLS = e.TLSCertEnc != ""
	return e, nil
}

// GetEndpoint returns one endpoint by id.
func (s *Store) GetEndpoint(ctx context.Context, id int64) (Endpoint, error) {
	return scanEndpoint(s.pool.QueryRow(ctx, `SELECT `+epCols+` FROM endpoints WHERE id = $1`, id))
}

// ListEndpoints returns all remote endpoints.
func (s *Store) ListEndpoints(ctx context.Context) ([]Endpoint, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+epCols+` FROM endpoints ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Endpoint
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateEndpoint updates an endpoint's name, host, and (already-encrypted) TLS
// material.
func (s *Store) UpdateEndpoint(ctx context.Context, e Endpoint) error {
	_, err := s.pool.Exec(ctx, `
UPDATE endpoints SET name = $1, host = $2, tls_ca_enc = $3, tls_cert_enc = $4, tls_key_enc = $5
WHERE id = $6`,
		e.Name, e.Host, e.TLSCAEnc, e.TLSCertEnc, e.TLSKeyEnc, e.ID)
	return err
}

// DeleteEndpoint removes an endpoint.
func (s *Store) DeleteEndpoint(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM endpoints WHERE id = $1`, id)
	return err
}

// --- per-user environment grants (access + per-environment quota) ---

// EndpointGrant is a member's access to one remote environment plus the CPU/RAM
// quota they may use there.
type EndpointGrant struct {
	EndpointID    int64 `json:"endpointId"`
	CPUQuotaMilli int   `json:"cpuQuotaMilli"`
	MemQuotaMB    int   `json:"memQuotaMB"`
}

// GetUserEndpointIDs returns the remote environment ids a user is granted.
func (s *Store) GetUserEndpointIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT endpoint_id FROM user_endpoints WHERE user_id = $1 ORDER BY endpoint_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GetUserEndpoints returns a user's environment grants with their per-env quota.
func (s *Store) GetUserEndpoints(ctx context.Context, userID int64) ([]EndpointGrant, error) {
	rows, err := s.pool.Query(ctx, `SELECT endpoint_id, cpu_quota_milli, mem_quota_mb FROM user_endpoints WHERE user_id = $1 ORDER BY endpoint_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EndpointGrant{}
	for rows.Next() {
		var g EndpointGrant
		if err := rows.Scan(&g.EndpointID, &g.CPUQuotaMilli, &g.MemQuotaMB); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UserHasEndpoint reports whether a user is granted a remote environment.
func (s *Store) UserHasEndpoint(ctx context.Context, userID, endpointID int64) (bool, error) {
	var one int
	err := s.pool.QueryRow(ctx, `SELECT 1 FROM user_endpoints WHERE user_id = $1 AND endpoint_id = $2`, userID, endpointID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// GetUserEndpointQuota returns a user's quota on a remote environment (ok=false
// if they have no grant there).
func (s *Store) GetUserEndpointQuota(ctx context.Context, userID, endpointID int64) (EndpointGrant, bool, error) {
	var g EndpointGrant
	g.EndpointID = endpointID
	err := s.pool.QueryRow(ctx, `SELECT cpu_quota_milli, mem_quota_mb FROM user_endpoints WHERE user_id = $1 AND endpoint_id = $2`, userID, endpointID).Scan(&g.CPUQuotaMilli, &g.MemQuotaMB)
	if errors.Is(err, pgx.ErrNoRows) {
		return EndpointGrant{}, false, nil
	}
	return g, err == nil, err
}

// SetUserEndpoints replaces a user's grants (access + per-env quota).
func (s *Store) SetUserEndpoints(ctx context.Context, userID int64, grants []EndpointGrant) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM user_endpoints WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, g := range grants {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_endpoints (user_id, endpoint_id, cpu_quota_milli, mem_quota_mb) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (user_id, endpoint_id) DO UPDATE SET cpu_quota_milli = EXCLUDED.cpu_quota_milli, mem_quota_mb = EXCLUDED.mem_quota_mb`,
			userID, g.EndpointID, g.CPUQuotaMilli, g.MemQuotaMB); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GrantUserEndpointQuota grants (or updates) a member's access + quota on one
// remote environment. Used when approving a resource request for that host.
func (s *Store) GrantUserEndpointQuota(ctx context.Context, userID, endpointID int64, cpuMilli, memMB int) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO user_endpoints (user_id, endpoint_id, cpu_quota_milli, mem_quota_mb) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, endpoint_id) DO UPDATE SET cpu_quota_milli = EXCLUDED.cpu_quota_milli, mem_quota_mb = EXCLUDED.mem_quota_mb`,
		userID, endpointID, cpuMilli, memMB)
	return err
}
