// Package registry manages container image registry credentials and the
// registry v2 HTTP API. Passwords are encrypted at rest via the secret box;
// credentials are turned into Docker-compatible auth tokens for pulls and
// used for catalog/tag browsing.
package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"easydeploy/internal/secret"
	"easydeploy/internal/store"

	registrytypes "github.com/docker/docker/api/types/registry"
)

// Service coordinates registry persistence, encryption, and API access.
type Service struct {
	store *store.Store
	box   *secret.Box
	http  *http.Client
}

// New creates a registry service.
func New(st *store.Store, box *secret.Box) *Service {
	return &Service{store: st, box: box, http: &http.Client{Timeout: 15 * time.Second}}
}

// List returns configured registries (passwords are never included).
func (s *Service) List(ctx context.Context) ([]store.Registry, error) {
	return s.store.ListRegistries(ctx)
}

// Create encrypts the password and stores a new registry.
func (s *Service) Create(ctx context.Context, name, url, username, password string) (store.Registry, error) {
	if name == "" || url == "" {
		return store.Registry{}, fmt.Errorf("name and url are required")
	}
	enc, err := s.box.Encrypt(password)
	if err != nil {
		return store.Registry{}, err
	}
	return s.store.InsertRegistry(ctx, store.Registry{
		Name:        name,
		URL:         normalizeHost(url),
		Username:    username,
		PasswordEnc: enc,
	})
}

// Delete removes a registry.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.store.DeleteRegistry(ctx, id)
}

// AuthForImage returns a base64 Docker auth token for the registry that hosts
// the given image, or "" if none matches (public image).
func (s *Service) AuthForImage(ctx context.Context, image string) (string, error) {
	host := hostForImage(image)
	regs, err := s.store.ListRegistries(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range regs {
		if hostsMatch(r.URL, host) {
			pw, err := s.box.Decrypt(r.PasswordEnc)
			if err != nil {
				return "", err
			}
			return encodeAuth(r.Username, pw, r.URL), nil
		}
	}
	return "", nil
}

// TestLogin verifies credentials against the registry's v2 endpoint.
func (s *Service) TestLogin(ctx context.Context, url, username, password string) error {
	host := normalizeHost(url)
	resp, err := s.do(ctx, host, "/v2/", username, password)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("registry returned %s", resp.Status)
}

// Catalog lists repositories in a registry.
func (s *Service) Catalog(ctx context.Context, id int64) ([]string, error) {
	r, pw, err := s.credsFor(ctx, id)
	if err != nil {
		return nil, err
	}
	resp, err := s.do(ctx, r.URL, "/v2/_catalog?n=100", r.Username, pw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog: registry returned %s", resp.Status)
	}
	var out struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Repositories, nil
}

// Tags lists tags for a repository in a registry.
func (s *Service) Tags(ctx context.Context, id int64, repo string) ([]string, error) {
	r, pw, err := s.credsFor(ctx, id)
	if err != nil {
		return nil, err
	}
	resp, err := s.do(ctx, r.URL, "/v2/"+repo+"/tags/list", r.Username, pw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tags: registry returned %s", resp.Status)
	}
	var out struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Tags, nil
}

func (s *Service) credsFor(ctx context.Context, id int64) (store.Registry, string, error) {
	regs, err := s.store.ListRegistries(ctx)
	if err != nil {
		return store.Registry{}, "", err
	}
	for _, r := range regs {
		if r.ID == id {
			pw, err := s.box.Decrypt(r.PasswordEnc)
			if err != nil {
				return store.Registry{}, "", err
			}
			return r, pw, nil
		}
	}
	return store.Registry{}, "", fmt.Errorf("registry %d not found", id)
}

// do performs a GET against the registry, trying https then http (for
// insecure/local registries), attaching basic auth when a username is set.
func (s *Service) do(ctx context.Context, host, path, username, password string) (*http.Response, error) {
	var lastErr error
	for _, scheme := range []string{"https", "http"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+host+path, nil)
		if err != nil {
			return nil, err
		}
		if username != "" {
			req.SetBasicAuth(username, password)
		}
		resp, err := s.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("registry unreachable: %w", lastErr)
}

// encodeAuth builds the base64 JSON auth token the Docker daemon expects for
// authenticated pulls.
func encodeAuth(username, password, serverAddress string) string {
	cfg := registrytypes.AuthConfig{
		Username:      username,
		Password:      password,
		ServerAddress: serverAddress,
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(b)
}

// --- image/host helpers ---

// normalizeHost strips any scheme and trailing slash from a registry URL,
// leaving just the host[:port].
func normalizeHost(url string) string {
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	return url
}

// hostForImage extracts the registry host from an image reference, defaulting
// to Docker Hub.
func hostForImage(image string) string {
	// Strip any digest/tag first is unnecessary for host detection.
	slash := strings.IndexByte(image, '/')
	if slash == -1 {
		return "registry-1.docker.io" // bare name like "nginx"
	}
	first := image[:slash]
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return first
	}
	return "registry-1.docker.io" // "library/nginx" style
}

// hostsMatch treats Docker Hub aliases as equivalent.
func hostsMatch(configured, imageHost string) bool {
	configured = normalizeHost(configured)
	if configured == imageHost {
		return true
	}
	hub := map[string]bool{"docker.io": true, "index.docker.io": true, "registry-1.docker.io": true}
	return hub[configured] && hub[imageHost]
}
