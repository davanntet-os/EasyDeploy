package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/pkg/archive"
)

// CPUPercent reads a single stats sample and computes the container's CPU
// usage as a percentage of one core * online CPUs (the same number `docker
// stats` shows). Used by the autoscaler.
func (c *Client) CPUPercent(ctx context.Context, id string) (float64, error) {
	resp, err := c.cli.ContainerStats(ctx, id, false)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var v types.StatsJSON
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return 0, err
	}
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)
	onlineCPUs := float64(v.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
	}
	if systemDelta <= 0 || onlineCPUs == 0 {
		return 0, nil
	}
	return (cpuDelta / systemDelta) * onlineCPUs * 100.0, nil
}

// BuildImage builds an image from a local context directory using the given
// Dockerfile (relative to the context) and tags it. The build output stream
// is forwarded line-by-line to onLog (may be nil). This backs the git webhook:
// the repo is cloned to contextDir first.
func (c *Client) BuildImage(ctx context.Context, contextDir, dockerfile, tag string, onLog func(string)) error {
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	tarCtx, err := archive.TarWithOptions(contextDir, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("tar build context: %w", err)
	}
	defer tarCtx.Close()

	resp, err := c.cli.ImageBuild(ctx, tarCtx, types.ImageBuildOptions{
		Dockerfile: dockerfile,
		Tags:       []string{tag},
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("image build: %w", err)
	}
	defer resp.Body.Close()

	// The build stream is newline-delimited JSON: {"stream":"..."} or
	// {"error":"..."}. Surface errors and forward log lines.
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("read build output: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("build failed: %s", msg.Error)
		}
		if msg.Stream != "" && onLog != nil {
			onLog(msg.Stream)
		}
	}
	return nil
}
