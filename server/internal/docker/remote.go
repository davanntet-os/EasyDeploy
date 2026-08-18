package docker

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/docker/cli/cli/connhelper"
	"github.com/docker/docker/client"
)

// TLSMaterial holds PEM-encoded client TLS credentials for a remote endpoint.
type TLSMaterial struct {
	CA   []byte // server CA to verify against (required for TLS)
	Cert []byte // client certificate
	Key  []byte // client private key
}

// NewRemote connects to a remote Docker daemon.
//
//   - "ssh://user@host[:port]" tunnels the Docker API over SSH (no open Docker
//     port; auth via the server's SSH keys/agent). This is the safest option.
//   - "tcp://host:2376" with tls uses mutual TLS. A CA is REQUIRED so the
//     server's identity is verified — we never skip verification.
//   - "tcp://host:2375" without tls is plaintext and unauthenticated; only use
//     it on a trusted, isolated network.
func NewRemote(host string, tlsMat *TLSMaterial) (*Client, error) {
	if host == "" {
		return nil, fmt.Errorf("remote endpoint host is required")
	}
	if strings.HasPrefix(host, "ssh://") {
		return newSSHClient(host)
	}

	opts := []client.Opt{client.WithHost(host), client.WithAPIVersionNegotiation()}

	if tlsMat != nil && len(tlsMat.Cert) > 0 {
		if len(tlsMat.CA) == 0 {
			return nil, fmt.Errorf("a CA certificate is required for TLS (refusing to skip server verification)")
		}
		cert, err := tls.X509KeyPair(tlsMat.Cert, tlsMat.Key)
		if err != nil {
			return nil, fmt.Errorf("tls keypair: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(tlsMat.CA) {
			return nil, fmt.Errorf("invalid CA certificate")
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      pool,
			MinVersion:   tls.VersionTLS12,
		}
		httpClient := &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
			Timeout:   30 * time.Second,
		}
		opts = append(opts, client.WithHTTPClient(httpClient))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("remote docker client: %w", err)
	}
	return &Client{cli: cli, volHelpers: map[string]string{}}, nil
}

// newSSHClient builds a client that reaches the Docker API over an SSH tunnel
// (via `docker system dial-stdio` on the remote). It uses the local `ssh`
// binary and its key/agent configuration, with connection multiplexing so
// every Docker API call reuses a single SSH connection instead of paying a
// fresh TCP+SSH handshake each time (the difference between ~1s and ~10ms).
func newSSHClient(host string) (*Client, error) {
	sshFlags := []string{
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=20",
		// Reuse one SSH connection for all subsequent calls (kept 5min idle).
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=/tmp/ed-ssh-%C",
		"-o", "ControlPersist=5m",
		// Don't hang on unknown hosts or password prompts in a server context.
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
	}
	helper, err := connhelper.GetConnectionHelperWithSSHOpts(host, sshFlags)
	if err != nil {
		return nil, fmt.Errorf("ssh connection helper: %w", err)
	}
	httpClient := &http.Client{Transport: &http.Transport{DialContext: helper.Dialer}}
	cli, err := client.NewClientWithOpts(
		client.WithHTTPClient(httpClient),
		client.WithHost(helper.Host),
		client.WithDialContext(helper.Dialer),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("ssh docker client: %w", err)
	}
	return &Client{cli: cli, volHelpers: map[string]string{}}, nil
}
