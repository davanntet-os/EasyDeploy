// Package endpoint manages the set of Docker environments EasyDeploy can
// operate on: the local host plus any remote Docker-API endpoints. It caches a
// docker.Client per environment and decrypts stored TLS material on demand.
package endpoint

import (
	"context"
	"fmt"
	"sync"
	"time"

	"easydeploy/internal/docker"
	"easydeploy/internal/secret"
	"easydeploy/internal/store"
)

// LocalID is the reserved id of the local host environment.
const LocalID int64 = 0

// Manager resolves environment ids to docker clients.
type Manager struct {
	store *store.Store
	box   *secret.Box
	local *docker.Client

	mu      sync.Mutex
	clients map[int64]*docker.Client
}

// New creates a manager over the local client.
func New(st *store.Store, box *secret.Box, local *docker.Client) *Manager {
	return &Manager{store: st, box: box, local: local, clients: map[int64]*docker.Client{}}
}

// Local returns the local host client (never nil).
func (m *Manager) Local() *docker.Client { return m.local }

// ClientFor returns the docker client for an environment id (0 = local).
func (m *Manager) ClientFor(ctx context.Context, id int64) (*docker.Client, error) {
	if id == LocalID {
		return m.local, nil
	}
	m.mu.Lock()
	if c, ok := m.clients[id]; ok {
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()

	ep, err := m.store.GetEndpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	var tlsMat *docker.TLSMaterial
	if ep.TLSCertEnc != "" {
		ca, _ := m.box.Decrypt(ep.TLSCAEnc)
		cert, _ := m.box.Decrypt(ep.TLSCertEnc)
		key, _ := m.box.Decrypt(ep.TLSKeyEnc)
		tlsMat = &docker.TLSMaterial{CA: []byte(ca), Cert: []byte(cert), Key: []byte(key)}
	}
	cli, err := docker.NewRemote(ep.Host, tlsMat)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.clients[id] = cli
	m.mu.Unlock()
	return cli, nil
}

// List returns the persisted remote endpoints.
func (m *Manager) List(ctx context.Context) ([]store.Endpoint, error) {
	return m.store.ListEndpoints(ctx)
}

// Create stores a new remote endpoint, encrypting any TLS material.
func (m *Manager) Create(ctx context.Context, name, host, caPEM, certPEM, keyPEM string) (store.Endpoint, error) {
	if name == "" || host == "" {
		return store.Endpoint{}, fmt.Errorf("name and host are required")
	}
	caEnc, _ := m.box.Encrypt(caPEM)
	certEnc, _ := m.box.Encrypt(certPEM)
	keyEnc, _ := m.box.Encrypt(keyPEM)
	return m.store.CreateEndpoint(ctx, store.Endpoint{
		Name: name, Host: host, TLSCAEnc: caEnc, TLSCertEnc: certEnc, TLSKeyEnc: keyEnc,
	})
}

// Update changes an endpoint's name, host, and optionally its TLS material,
// then drops the cached client so the next request rebuilds the connection.
// Empty cert material preserves the existing certs (so an SSH/name-only edit
// doesn't wipe stored TLS).
func (m *Manager) Update(ctx context.Context, id int64, name, host, caPEM, certPEM, keyPEM string) (store.Endpoint, error) {
	if name == "" || host == "" {
		return store.Endpoint{}, fmt.Errorf("name and host are required")
	}
	cur, err := m.store.GetEndpoint(ctx, id)
	if err != nil {
		return store.Endpoint{}, err
	}
	caEnc, certEnc, keyEnc := cur.TLSCAEnc, cur.TLSCertEnc, cur.TLSKeyEnc
	if certPEM != "" { // new TLS material supplied → re-encrypt all three
		caEnc, _ = m.box.Encrypt(caPEM)
		certEnc, _ = m.box.Encrypt(certPEM)
		keyEnc, _ = m.box.Encrypt(keyPEM)
	}
	ep := store.Endpoint{ID: id, Name: name, Host: host, TLSCAEnc: caEnc, TLSCertEnc: certEnc, TLSKeyEnc: keyEnc}
	if err := m.store.UpdateEndpoint(ctx, ep); err != nil {
		return store.Endpoint{}, err
	}
	m.mu.Lock()
	delete(m.clients, id) // rebuild on next use
	m.mu.Unlock()
	ep.TLS = certEnc != ""
	ep.CreatedAt = cur.CreatedAt
	return ep, nil
}

// Delete removes an endpoint and drops any cached client.
func (m *Manager) Delete(ctx context.Context, id int64) error {
	m.mu.Lock()
	delete(m.clients, id)
	m.mu.Unlock()
	return m.store.DeleteEndpoint(ctx, id)
}

// Status pings an environment and reports reachability + server version. It is
// time-bounded so an unreachable host (e.g. a dead SSH target) never hangs.
func (m *Manager) Status(ctx context.Context, id int64) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cli, err := m.ClientFor(ctx, id)
	if err != nil {
		return false, ""
	}
	v, err := cli.ServerVersion(ctx)
	if err != nil {
		return false, ""
	}
	return true, v
}
