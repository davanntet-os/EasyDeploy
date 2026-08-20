package store

import "context"

// VolumeSpec records the app-level size budget for a managed volume on a given
// environment. Docker local volumes are not byte-capped, so this budget is what
// the storage quota is enforced against.
type VolumeSpec struct {
	EndpointID int64  `json:"endpointId"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	SizeMB     int    `json:"sizeMB"`
}

// SetVolumeSize upserts a volume's size budget and owner.
func (s *Store) SetVolumeSize(ctx context.Context, endpointID int64, name, owner string, sizeMB int) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO volume_specs (endpoint_id, name, owner, size_mb) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (endpoint_id, name) DO UPDATE SET size_mb = EXCLUDED.size_mb, owner = EXCLUDED.owner`,
		endpointID, name, owner, sizeMB)
	return err
}

// DeleteVolumeSize removes a volume's size record (on volume delete).
func (s *Store) DeleteVolumeSize(ctx context.Context, endpointID int64, name string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM volume_specs WHERE endpoint_id = $1 AND name = $2`, endpointID, name)
	return err
}

// ListVolumeSizes returns name -> size_mb for every recorded volume on an env.
func (s *Store) ListVolumeSizes(ctx context.Context, endpointID int64) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, size_mb FROM volume_specs WHERE endpoint_id = $1`, endpointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var size int
		if err := rows.Scan(&name, &size); err != nil {
			return nil, err
		}
		out[name] = size
	}
	return out, rows.Err()
}

// OwnerDiskUsageMB sums a member's volume-size budgets on an environment — the
// storage quota is enforced against this. When excludeName is non-empty that
// volume is left out (so resizing a volume isn't counted against itself).
func (s *Store) OwnerDiskUsageMB(ctx context.Context, endpointID int64, owner, excludeName string) (int, error) {
	var total int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(size_mb), 0) FROM volume_specs
		 WHERE endpoint_id = $1 AND owner = $2 AND name <> $3`,
		endpointID, owner, excludeName).Scan(&total)
	return total, err
}
