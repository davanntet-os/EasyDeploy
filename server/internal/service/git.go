package service

import (
	"context"
	"fmt"
	"log"
	"os"

	"easydeploy/internal/store"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// BuildAndRedeploy clones the service's git repo at its branch, builds an
// image from its Dockerfile, and rolls the new image out to all replicas.
// This backs the git webhook. It is safe to run in a background goroutine.
func (m *Manager) BuildAndRedeploy(ctx context.Context, svc store.Service) error {
	if svc.GitRepo == "" {
		return fmt.Errorf("service %q has no git repo configured", svc.Name)
	}
	dir, err := os.MkdirTemp("", "easydeploy-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	branch := svc.GitBranch
	if branch == "" {
		branch = "main"
	}
	log.Printf("service %s: cloning %s@%s", svc.Name, svc.GitRepo, branch)
	_, err = git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{
		URL:           svc.GitRepo,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Depth:         1,
	})
	if err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	cli, err := m.dockerFor(ctx, svc)
	if err != nil {
		return err
	}
	tag := fmt.Sprintf("easydeploy/%s:latest", svc.Name)
	log.Printf("service %s: building image %s", svc.Name, tag)
	if err := cli.BuildImage(ctx, dir, svc.GitDockerfile, tag, func(line string) {
		// Build output is chatty; keep it at debug volume in the server log.
	}); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	log.Printf("service %s: rolling out %s", svc.Name, tag)
	return m.Redeploy(ctx, svc.Name, tag)
}
