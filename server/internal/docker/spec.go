package docker

import (
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/go-connections/nat"
)

// PortMap is a single published port mapping (like `-p`).
type PortMap struct {
	HostIP        string `json:"hostIp"`        // optional bind IP (default 0.0.0.0)
	HostPort      string `json:"hostPort"`      // host port ("" = ephemeral)
	ContainerPort string `json:"containerPort"` // required, e.g. "80"
	Proto         string `json:"proto"`         // tcp|udp (default tcp)
}

// VolumeMount is a bind mount, named volume, or tmpfs (like `-v`/`--mount`).
type VolumeMount struct {
	Type     string `json:"type"`     // bind|volume|tmpfs
	Source   string `json:"source"`   // host path or volume name (empty for tmpfs)
	Target   string `json:"target"`   // path inside the container
	ReadOnly bool   `json:"readOnly"` // mount read-only
}

// HealthCheck configures a container healthcheck (like `--health-*`).
type HealthCheck struct {
	Test        []string `json:"test"`           // e.g. ["CMD-SHELL","curl -f http://localhost/ || exit 1"]
	IntervalSec int      `json:"intervalSec"`    //
	TimeoutSec  int      `json:"timeoutSec"`     //
	Retries     int      `json:"retries"`        //
	StartSec    int      `json:"startPeriodSec"` //
}

// AdvancedSpec holds the full breadth of container options beyond the common
// ones. It is embedded in DeploySpec and persisted per-service as JSON, so the
// service form can expose "everything docker run can do".
type AdvancedSpec struct {
	Ports          []PortMap         `json:"ports"`
	Mounts         []VolumeMount     `json:"mounts"`
	Command        []string          `json:"command"`
	Entrypoint     []string          `json:"entrypoint"`
	WorkingDir     string            `json:"workingDir"`
	User           string            `json:"user"`
	Hostname       string            `json:"hostname"`
	Labels         map[string]string `json:"labels"`
	ExtraHosts     []string          `json:"extraHosts"` // "host:ip"
	RestartPolicy  string            `json:"restartPolicy"`
	RestartRetries int               `json:"restartRetries"`
	CapAdd         []string          `json:"capAdd"`
	CapDrop        []string          `json:"capDrop"`
	Privileged     bool              `json:"privileged"`
	ReadonlyRootfs bool              `json:"readonlyRootfs"`
	Init           bool              `json:"init"`
	DNS            []string          `json:"dns"`
	Devices        []string          `json:"devices"` // "host:container[:perms]"
	Sysctls        map[string]string `json:"sysctls"`
	Tmpfs          map[string]string `json:"tmpfs"` // path -> options
	PidsLimit      int64             `json:"pidsLimit"`
	MemorySwapMB   int               `json:"memorySwapMB"`
	CPUShares      int64             `json:"cpuShares"`
	StopSignal     string            `json:"stopSignal"`
	StopTimeoutSec int               `json:"stopTimeoutSec"`
	LogDriver      string            `json:"logDriver"`
	LogOpts        map[string]string `json:"logOpts"`
	Health         *HealthCheck      `json:"health"`
}

// applyAdvanced maps the advanced spec onto the container config and host
// config. It is additive: zero-valued fields leave Docker defaults in place.
func applyAdvanced(cfg *container.Config, hostCfg *container.HostConfig, s AdvancedSpec) error {
	// --- container.Config ---
	if len(s.Command) > 0 {
		cfg.Cmd = strslice.StrSlice(s.Command)
	}
	if len(s.Entrypoint) > 0 {
		cfg.Entrypoint = strslice.StrSlice(s.Entrypoint)
	}
	cfg.WorkingDir = orString(cfg.WorkingDir, s.WorkingDir)
	cfg.User = orString(cfg.User, s.User)
	cfg.Hostname = orString(cfg.Hostname, s.Hostname)
	cfg.StopSignal = orString(cfg.StopSignal, s.StopSignal)
	for k, v := range s.Labels {
		cfg.Labels[k] = v
	}
	if s.StopTimeoutSec > 0 {
		t := s.StopTimeoutSec
		cfg.StopTimeout = &t
	}
	if s.Health != nil && len(s.Health.Test) > 0 {
		cfg.Healthcheck = &container.HealthConfig{
			Test:        s.Health.Test,
			Interval:    time.Duration(s.Health.IntervalSec) * time.Second,
			Timeout:     time.Duration(s.Health.TimeoutSec) * time.Second,
			Retries:     s.Health.Retries,
			StartPeriod: time.Duration(s.Health.StartSec) * time.Second,
		}
	}

	// --- extra published ports ---
	for _, p := range s.Ports {
		if p.ContainerPort == "" {
			continue
		}
		proto := p.Proto
		if proto == "" {
			proto = "tcp"
		}
		port, err := nat.NewPort(proto, p.ContainerPort)
		if err != nil {
			return err
		}
		if cfg.ExposedPorts == nil {
			cfg.ExposedPorts = nat.PortSet{}
		}
		cfg.ExposedPorts[port] = struct{}{}
		hostIP := p.HostIP
		if hostIP == "" {
			hostIP = "0.0.0.0"
		}
		hostCfg.PortBindings[port] = append(hostCfg.PortBindings[port], nat.PortBinding{HostIP: hostIP, HostPort: p.HostPort})
	}

	// --- mounts (bind / volume / tmpfs) ---
	for _, m := range s.Mounts {
		if m.Target == "" {
			continue
		}
		mt := mount.TypeBind
		switch m.Type {
		case "volume":
			mt = mount.TypeVolume
		case "tmpfs":
			mt = mount.TypeTmpfs
		}
		hostCfg.Mounts = append(hostCfg.Mounts, mount.Mount{
			Type:     mt,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	// --- host config knobs ---
	if s.RestartPolicy != "" {
		hostCfg.RestartPolicy = container.RestartPolicy{
			Name:              container.RestartPolicyMode(s.RestartPolicy),
			MaximumRetryCount: s.RestartRetries,
		}
	}
	if len(s.CapAdd) > 0 {
		hostCfg.CapAdd = strslice.StrSlice(s.CapAdd)
	}
	if len(s.CapDrop) > 0 {
		hostCfg.CapDrop = strslice.StrSlice(s.CapDrop)
	}
	hostCfg.Privileged = hostCfg.Privileged || s.Privileged
	hostCfg.ReadonlyRootfs = hostCfg.ReadonlyRootfs || s.ReadonlyRootfs
	if s.Init {
		init := true
		hostCfg.Init = &init
	}
	if len(s.DNS) > 0 {
		hostCfg.DNS = s.DNS
	}
	if len(s.ExtraHosts) > 0 {
		hostCfg.ExtraHosts = s.ExtraHosts
	}
	if len(s.Sysctls) > 0 {
		hostCfg.Sysctls = s.Sysctls
	}
	if len(s.Tmpfs) > 0 {
		hostCfg.Tmpfs = s.Tmpfs
	}
	for _, d := range s.Devices {
		hostCfg.Devices = append(hostCfg.Devices, parseDevice(d))
	}
	if s.PidsLimit > 0 {
		hostCfg.Resources.PidsLimit = &s.PidsLimit
	}
	if s.MemorySwapMB > 0 {
		hostCfg.Resources.MemorySwap = int64(s.MemorySwapMB) << 20
	}
	if s.CPUShares > 0 {
		hostCfg.Resources.CPUShares = s.CPUShares
	}
	if s.LogDriver != "" {
		hostCfg.LogConfig = container.LogConfig{Type: s.LogDriver, Config: s.LogOpts}
	}
	return nil
}

func orString(cur, next string) string {
	if next != "" {
		return next
	}
	return cur
}

// parseDevice parses "host[:container[:perms]]" into a DeviceMapping.
func parseDevice(spec string) container.DeviceMapping {
	host, cont, perms := spec, spec, "rwm"
	parts := splitColon(spec)
	switch len(parts) {
	case 1:
		host, cont = parts[0], parts[0]
	case 2:
		host, cont = parts[0], parts[1]
	case 3:
		host, cont, perms = parts[0], parts[1], parts[2]
	}
	return container.DeviceMapping{PathOnHost: host, PathInContainer: cont, CgroupPermissions: perms}
}

func splitColon(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ':' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
