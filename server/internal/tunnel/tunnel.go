// Package tunnel handles public exposure of the Envoy proxy. Two strategies
// are supported:
//
//   - WiFi: discover the local network's NAT public IP so the user can set
//     up port-forwarding on their router manually.
//   - SSH: open an SSH reverse tunnel to a cloud VM, binding a remote port
//     that forwards inbound traffic back to the local Envoy listener. This
//     gives a stable public IP without touching the local router.
package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"easydeploy/internal/store"
	"golang.org/x/crypto/ssh"
)

// Manager runs and tracks active tunnels.
type Manager struct {
	mu      sync.Mutex
	running map[int64]context.CancelFunc
	keyPath string
}

// NewManager creates a tunnel manager. keyPath is the SSH private key used
// for reverse tunnels; if empty it falls back to $EASYDEPLOY_SSH_KEY.
func NewManager(keyPath string) *Manager {
	if keyPath == "" {
		keyPath = os.Getenv("EASYDEPLOY_SSH_KEY")
	}
	return &Manager{running: make(map[int64]context.CancelFunc), keyPath: keyPath}
}

// PublicIP returns the current NAT/WiFi public IP by querying external echo
// services. The configured service is tried first, then a list of fallbacks
// that includes plaintext HTTP endpoints — these survive TLS-inspecting
// corporate proxies that reject direct HTTPS handshakes.
func PublicIP(ctx context.Context, service string) (string, error) {
	candidates := []string{service,
		"http://api.ipify.org",
		"http://icanhazip.com",
		"http://ifconfig.me/ip",
		"https://icanhazip.com",
		"https://ipinfo.io/ip",
	}
	var lastErr error
	for _, url := range candidates {
		if url == "" {
			continue
		}
		ip, err := fetchIP(ctx, url)
		if err != nil {
			lastErr = err
			continue
		}
		return ip, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no public IP service configured")
	}
	return "", fmt.Errorf("all public IP services failed; last error: %w", lastErr)
}

func fetchIP(ctx context.Context, url string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	// A curl-like UA makes services such as ifconfig.me return a bare IP
	// instead of an HTML page.
	req.Header.Set("User-Agent", "curl/8.0")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("%s returned non-IP response: %q", url, ip)
	}
	return ip, nil
}

// Start opens an SSH reverse tunnel described by t and tracks it under its
// ID. The tunnel runs until Stop is called or the process exits. It returns
// once the remote listener is established.
func (m *Manager) Start(ctx context.Context, t store.Tunnel) error {
	if t.Kind != store.TunnelSSH {
		return fmt.Errorf("only ssh tunnels can be started (got %q)", t.Kind)
	}
	m.mu.Lock()
	if _, ok := m.running[t.ID]; ok {
		m.mu.Unlock()
		return fmt.Errorf("tunnel %d already running", t.ID)
	}
	m.mu.Unlock()

	signer, err := m.loadSigner()
	if err != nil {
		return err
	}
	sshCfg := &ssh.ClientConfig{
		User:            t.SSHUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: pin known_hosts
		Timeout:         15 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", t.SSHHost, t.SSHPort)
	conn, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	remoteBind := fmt.Sprintf("0.0.0.0:%d", t.RemotePort)
	listener, err := conn.Listen("tcp", remoteBind)
	if err != nil {
		conn.Close()
		return fmt.Errorf("remote listen %s: %w", remoteBind, err)
	}

	tunCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.running[t.ID] = cancel
	m.mu.Unlock()

	localAddr := fmt.Sprintf("127.0.0.1:%d", t.LocalPort)
	go m.serve(tunCtx, listener, conn, localAddr)
	return nil
}

// serve accepts remote connections and pipes each to the local Envoy port.
func (m *Manager) serve(ctx context.Context, listener net.Listener, conn *ssh.Client, localAddr string) {
	go func() {
		<-ctx.Done()
		listener.Close()
		conn.Close()
	}()
	for {
		remote, err := listener.Accept()
		if err != nil {
			return // context cancelled, or listener closed/errored
		}
		go pipe(remote, localAddr)
	}
}

func pipe(remote net.Conn, localAddr string) {
	defer remote.Close()
	local, err := net.DialTimeout("tcp", localAddr, 10*time.Second)
	if err != nil {
		return
	}
	defer local.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(local, remote); done <- struct{}{} }()
	go func() { io.Copy(remote, local); done <- struct{}{} }()
	<-done
}

// Stop terminates a running tunnel by ID. It is a no-op if not running.
func (m *Manager) Stop(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.running[id]; ok {
		cancel()
		delete(m.running, id)
	}
}

// IsRunning reports whether a tunnel is currently active.
func (m *Manager) IsRunning(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.running[id]
	return ok
}

func (m *Manager) loadSigner() (ssh.Signer, error) {
	if m.keyPath == "" {
		return nil, fmt.Errorf("no SSH key configured (set EASYDEPLOY_SSH_KEY)")
	}
	key, err := os.ReadFile(m.keyPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse ssh key: %w", err)
	}
	return signer, nil
}
