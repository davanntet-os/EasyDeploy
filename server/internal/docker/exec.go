package docker

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// Exec starts an interactive command (a shell) inside a container with a TTY
// allocated and returns the hijacked connection for bidirectional I/O plus the
// exec instance ID (for resizing). The caller must Close the returned
// connection. With a TTY the stream is raw (not stdcopy-multiplexed).
func (c *Client) Exec(ctx context.Context, id string, cmd []string) (types.HijackedResponse, string, error) {
	created, err := c.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          cmd,
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return types.HijackedResponse{}, "", err
	}
	hijack, err := c.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return types.HijackedResponse{}, "", err
	}
	return hijack, created.ID, nil
}

// ExecResize adjusts the TTY size of a running exec instance.
func (c *Client) ExecResize(ctx context.Context, execID string, rows, cols uint) error {
	return c.cli.ContainerExecResize(ctx, execID, container.ResizeOptions{Height: rows, Width: cols})
}
