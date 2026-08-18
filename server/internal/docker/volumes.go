package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const (
	volHelperImage = "alpine:latest"
	volMount       = "/mnt" // where the volume is mounted inside the helper
)

// CreateVolume creates a named volume.
func (c *Client) CreateVolume(ctx context.Context, name, driver string, labels map[string]string) (volume.Volume, error) {
	if driver == "" {
		driver = "local"
	}
	return c.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name, Driver: driver, Labels: labels})
}

// RemoveVolume deletes a volume. force removes it even if it reports as in use.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	c.releaseHelper(ctx, name)
	return c.cli.VolumeRemove(ctx, name, force)
}

// InspectVolume returns full details for a volume.
func (c *Client) InspectVolume(ctx context.Context, name string) (volume.Volume, error) {
	return c.cli.VolumeInspect(ctx, name)
}

// VolumeUsage returns per-volume size (bytes) and reference count via the
// daemon's disk-usage report. It is scoped to volumes only — without the
// filter the daemon also computes image/container/build-cache usage, which is
// far slower on a busy host.
func (c *Client) VolumeUsage(ctx context.Context) (map[string]volume.UsageData, error) {
	du, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{Types: []types.DiskUsageObject{types.VolumeObject}})
	if err != nil {
		return nil, err
	}
	out := make(map[string]volume.UsageData, len(du.Volumes))
	for _, v := range du.Volumes {
		if v.UsageData != nil {
			out[v.Name] = *v.UsageData
		}
	}
	return out, nil
}

// VolFile is one entry in a volume directory listing.
type VolFile struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

// --- helper container (kept alive per volume for fast file operations) ---

// volumeHelper returns the id of a running helper container that has the volume
// mounted at /mnt, creating (and caching) one if needed. Reusing it makes
// browse/mkdir/upload/download fast (~exec latency) instead of paying
// container-create cost each call.
func (c *Client) volumeHelper(ctx context.Context, name string) (string, error) {
	c.volMu.Lock()
	defer c.volMu.Unlock()

	if id, ok := c.volHelpers[name]; ok {
		if info, err := c.cli.ContainerInspect(ctx, id); err == nil && info.State != nil && info.State.Running {
			return id, nil
		}
		delete(c.volHelpers, name)
	}

	cname := "easydeploy-volhelper-" + sanitizeName(name)
	// Reuse an existing helper (survives server restarts) or clear a dead one.
	if info, err := c.cli.ContainerInspect(ctx, cname); err == nil {
		if info.State != nil && info.State.Running {
			c.volHelpers[name] = info.ID
			return info.ID, nil
		}
		_ = c.cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true})
	}

	cfg := &container.Config{
		Image:  volHelperImage,
		Cmd:    []string{"sleep", "infinity"},
		Labels: map[string]string{"easydeploy.volhelper": "true"},
	}
	hostCfg := &container.HostConfig{
		// No network needed for file ops — skipping it avoids network setup
		// latency and works offline / behind restrictive proxies.
		NetworkMode: "none",
		Mounts:      []mount.Mount{{Type: mount.TypeVolume, Source: name, Target: volMount}},
	}
	// Create directly; only pay the (possibly slow) image pull if the image is
	// actually missing locally.
	created, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, cname)
	if client.IsErrNotFound(err) {
		if rc, perr := c.PullImage(ctx, volHelperImage); perr == nil {
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		}
		created, err = c.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, cname)
	}
	if err != nil {
		return "", fmt.Errorf("create volume helper: %w", err)
	}
	if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start volume helper: %w", err)
	}
	c.volHelpers[name] = created.ID
	return created.ID, nil
}

// releaseHelper removes a volume's helper container (before deleting the
// volume, which would otherwise be "in use").
func (c *Client) releaseHelper(ctx context.Context, name string) {
	c.volMu.Lock()
	id := c.volHelpers[name]
	delete(c.volHelpers, name)
	c.volMu.Unlock()
	if id == "" {
		id = "easydeploy-volhelper-" + sanitizeName(name)
	}
	_ = c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

// CleanupVolumeHelpers removes all lingering helper containers (call on shutdown).
func (c *Client) CleanupVolumeHelpers(ctx context.Context) {
	list, err := c.ListByLabel(ctx, "easydeploy.volhelper=true", true)
	if err != nil {
		return
	}
	for _, ct := range list {
		_ = c.cli.ContainerRemove(ctx, ct.ID, container.RemoveOptions{Force: true})
	}
}

// execCapture runs a command in a container and returns stdout, stderr, and the
// exit code.
func (c *Client) execCapture(ctx context.Context, id string, cmd []string) (string, string, int, error) {
	ex, err := c.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return "", "", 0, err
	}
	att, err := c.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", "", 0, err
	}
	defer att.Close()
	var out, errb bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &errb, att.Reader); err != nil {
		return "", "", 0, err
	}
	insp, _ := c.cli.ContainerExecInspect(ctx, ex.ID)
	return out.String(), errb.String(), insp.ExitCode, nil
}

// safePath cleans a user path so it cannot escape the volume root, returning
// the path inside the helper (under /mnt). Root maps to the mount itself with
// no trailing slash, so callers can compare against volMount.
func safePath(sub string) string {
	clean := path.Clean("/" + strings.TrimSpace(sub))
	if clean == "/" {
		return volMount
	}
	return volMount + clean
}

// --- file operations ---

// BrowseVolume lists a directory inside a volume.
func (c *Client) BrowseVolume(ctx context.Context, name, sub string) ([]VolFile, error) {
	id, err := c.volumeHelper(ctx, name)
	if err != nil {
		return nil, err
	}
	target := safePath(sub)
	script := fmt.Sprintf(
		`cd %q 2>/dev/null || exit 0; for e in * .[!.]* ..?*; do [ -e "$e" ] || continue; `+
			`if [ -d "$e" ]; then echo "d|0|$e"; else echo "f|$(wc -c < "$e" 2>/dev/null || echo 0)|$e"; fi; done`,
		target)
	out, _, _, err := c.execCapture(ctx, id, []string{"sh", "-c", script})
	if err != nil {
		return nil, err
	}
	return parseVolListing(out), nil
}

// MkdirVolume creates a directory (and parents) inside a volume.
func (c *Client) MkdirVolume(ctx context.Context, name, sub string) error {
	id, err := c.volumeHelper(ctx, name)
	if err != nil {
		return err
	}
	_, stderr, code, err := c.execCapture(ctx, id, []string{"mkdir", "-p", safePath(sub)})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("mkdir failed: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// DeleteInVolume removes a file or directory (recursively) inside a volume.
func (c *Client) DeleteInVolume(ctx context.Context, name, sub string) error {
	target := safePath(sub)
	if target == volMount { // never delete the mount root
		return fmt.Errorf("refusing to delete volume root")
	}
	id, err := c.volumeHelper(ctx, name)
	if err != nil {
		return err
	}
	_, stderr, code, err := c.execCapture(ctx, id, []string{"rm", "-rf", target})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("delete failed: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// UploadToVolume writes one uploaded file into a directory inside a volume,
// streaming it through Docker's archive API.
func (c *Client) UploadToVolume(ctx context.Context, name, dir, filename string, r io.Reader, size int64) error {
	id, err := c.volumeHelper(ctx, name)
	if err != nil {
		return err
	}
	filename = path.Base(filename) // strip any path components
	if filename == "" || filename == "." || filename == "/" {
		return fmt.Errorf("invalid filename")
	}
	body, err := io.ReadAll(io.LimitReader(r, size))
	if err != nil {
		return err
	}
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{Name: filename, Mode: 0o644, Size: int64(len(body))}); err != nil {
		return err
	}
	if _, err := tw.Write(body); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return c.cli.CopyToContainer(ctx, id, safePath(dir), &tarBuf, container.CopyToContainerOptions{})
}

// VolDownload is an open file stream from a volume.
type VolDownload struct {
	io.ReadCloser
	Name string
	Size int64
}

// DownloadFromVolume opens a file inside a volume for reading (streamed via
// Docker's archive API, extracting the single file from the returned tar).
func (c *Client) DownloadFromVolume(ctx context.Context, name, file string) (*VolDownload, error) {
	id, err := c.volumeHelper(ctx, name)
	if err != nil {
		return nil, err
	}
	rc, _, err := c.cli.CopyFromContainer(ctx, id, safePath(file))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			rc.Close()
			return nil, fmt.Errorf("not a file")
		}
		if err != nil {
			rc.Close()
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg {
			return &VolDownload{
				ReadCloser: struct {
					io.Reader
					io.Closer
				}{Reader: tr, Closer: rc},
				Name: path.Base(hdr.Name),
				Size: hdr.Size,
			}, nil
		}
	}
}

// --- helpers ---

func parseVolListing(out string) []VolFile {
	var files []VolFile
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(parts[1], 10, 64)
		files = append(files, VolFile{Dir: parts[0] == "d", Size: size, Name: parts[2]})
	}
	return files
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}
