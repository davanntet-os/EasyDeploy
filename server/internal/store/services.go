package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Service is a replica-managed application: EasyDeploy runs N identical
// replica containers behind one subdomain (Envoy load-balances across them),
// optionally autoscaling on CPU and redeploying from a git repo on webhook.
type Service struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Owner         string     `json:"owner"`
	Image         string     `json:"image"`
	Subdomain     string     `json:"subdomain"`
	ContainerPort int        `json:"containerPort"`
	Network       string     `json:"network"`
	Env           string     `json:"env"` // JSON-encoded []string
	CPUMilli      int        `json:"cpuMilli"`
	MemMB         int        `json:"memMB"`
	Replicas      int        `json:"replicas"`
	MinReplicas   int        `json:"minReplicas"`
	MaxReplicas   int        `json:"maxReplicas"`
	Autoscale     bool       `json:"autoscale"`
	TargetCPUPct  int        `json:"targetCpuPercent"`
	GitRepo       string     `json:"gitRepo"`
	GitBranch     string     `json:"gitBranch"`
	GitDockerfile string     `json:"gitDockerfile"`
	WebhookToken  string     `json:"webhookToken"`
	LastImage     string     `json:"lastImage"`
	LastDeployAt  *time.Time `json:"lastDeployAt"`
	Advanced      string     `json:"-"` // JSON-encoded docker.AdvancedSpec
	// EndpointID is the environment (Docker host) the service's replicas run on
	// (0 = local). It selects the Docker client used to reconcile replicas and
	// the Envoy node the load-balanced route is published to.
	EndpointID int64     `json:"endpointId"`
	CreatedAt  time.Time `json:"createdAt"`
}

const svcCols = `id, name, owner, image, subdomain, container_port, network, env,
	cpu_milli, mem_mb, replicas, min_replicas, max_replicas, autoscale,
	target_cpu_percent, git_repo, git_branch, git_dockerfile, webhook_token,
	last_image, last_deploy_at, advanced, endpoint_id, created_at`

func scanService(row pgx.Row) (Service, error) {
	var s Service
	err := row.Scan(&s.ID, &s.Name, &s.Owner, &s.Image, &s.Subdomain, &s.ContainerPort,
		&s.Network, &s.Env, &s.CPUMilli, &s.MemMB, &s.Replicas, &s.MinReplicas,
		&s.MaxReplicas, &s.Autoscale, &s.TargetCPUPct, &s.GitRepo, &s.GitBranch,
		&s.GitDockerfile, &s.WebhookToken, &s.LastImage, &s.LastDeployAt, &s.Advanced, &s.EndpointID, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	return s, err
}

// CreateService inserts a new service.
func (st *Store) CreateService(ctx context.Context, s Service) (Service, error) {
	if s.Advanced == "" {
		s.Advanced = "{}"
	}
	err := st.pool.QueryRow(ctx, `
INSERT INTO services (name, owner, image, subdomain, container_port, network, env,
	cpu_milli, mem_mb, replicas, min_replicas, max_replicas, autoscale,
	target_cpu_percent, git_repo, git_branch, git_dockerfile, webhook_token, advanced, endpoint_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
RETURNING id, created_at`,
		s.Name, s.Owner, s.Image, s.Subdomain, s.ContainerPort, s.Network, s.Env,
		s.CPUMilli, s.MemMB, s.Replicas, s.MinReplicas, s.MaxReplicas, s.Autoscale,
		s.TargetCPUPct, s.GitRepo, s.GitBranch, s.GitDockerfile, s.WebhookToken, s.Advanced, s.EndpointID,
	).Scan(&s.ID, &s.CreatedAt)
	if err != nil {
		return Service{}, err
	}
	return s, nil
}

// GetService looks up a service by name.
func (st *Store) GetService(ctx context.Context, name string) (Service, error) {
	return scanService(st.pool.QueryRow(ctx, `SELECT `+svcCols+` FROM services WHERE name = $1`, name))
}

// GetServiceByToken looks up a service by its webhook token.
func (st *Store) GetServiceByToken(ctx context.Context, token string) (Service, error) {
	return scanService(st.pool.QueryRow(ctx, `SELECT `+svcCols+` FROM services WHERE webhook_token = $1`, token))
}

// ListServices returns all services (or only a given owner's when owner != "").
func (st *Store) ListServices(ctx context.Context, owner string) ([]Service, error) {
	q := `SELECT ` + svcCols + ` FROM services`
	var args []any
	if owner != "" {
		q += ` WHERE owner = $1`
		args = append(args, owner)
	}
	q += ` ORDER BY name`
	rows, err := st.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Service
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetServiceReplicas updates the desired replica count.
func (st *Store) SetServiceReplicas(ctx context.Context, id int64, replicas int) error {
	_, err := st.pool.Exec(ctx, `UPDATE services SET replicas = $1 WHERE id = $2`, replicas, id)
	return err
}

// UpdateService updates a service's editable configuration in place (identity
// fields — id, name, owner, webhook token, endpoint — are not touched here).
func (st *Store) UpdateService(ctx context.Context, s Service) error {
	if s.Advanced == "" {
		s.Advanced = "{}"
	}
	_, err := st.pool.Exec(ctx, `
UPDATE services SET
	image=$1, subdomain=$2, container_port=$3, network=$4, env=$5,
	cpu_milli=$6, mem_mb=$7, replicas=$8, min_replicas=$9, max_replicas=$10,
	autoscale=$11, target_cpu_percent=$12, git_repo=$13, git_branch=$14,
	git_dockerfile=$15, advanced=$16
WHERE id=$17`,
		s.Image, s.Subdomain, s.ContainerPort, s.Network, s.Env,
		s.CPUMilli, s.MemMB, s.Replicas, s.MinReplicas, s.MaxReplicas,
		s.Autoscale, s.TargetCPUPct, s.GitRepo, s.GitBranch,
		s.GitDockerfile, s.Advanced, s.ID)
	return err
}

// SetServiceSubdomain updates the optional custom subdomain.
func (st *Store) SetServiceSubdomain(ctx context.Context, id int64, subdomain string) error {
	_, err := st.pool.Exec(ctx, `UPDATE services SET subdomain = $1 WHERE id = $2`, subdomain, id)
	return err
}

// SetServiceImage records the image after a (re)deploy.
func (st *Store) SetServiceImage(ctx context.Context, id int64, image string) error {
	_, err := st.pool.Exec(ctx, `UPDATE services SET image = $1, last_image = $1, last_deploy_at = now() WHERE id = $2`, image, id)
	return err
}

// DeleteService removes a service row.
func (st *Store) DeleteService(ctx context.Context, id int64) error {
	_, err := st.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, id)
	return err
}
