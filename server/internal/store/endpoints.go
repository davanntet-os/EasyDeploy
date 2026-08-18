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

// DeleteEndpoint removes an endpoint.
func (s *Store) DeleteEndpoint(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM endpoints WHERE id = $1`, id)
	return err
}
