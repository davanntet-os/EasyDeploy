package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Role enumerates the access levels.
type Role string

const (
	// RoleAdmin has full control and approves resource requests.
	RoleAdmin Role = "admin"
	// RoleMember must be granted a CPU/RAM quota before deploying, and every
	// deploy is capped to stay within it.
	RoleMember Role = "member"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// User is an account. Quotas are the total resources a member may consume
// across all their running containers (ignored for admins).
type User struct {
	ID            int64     `json:"id"`
	Username      string    `json:"username"`
	PasswordHash  string    `json:"-"`
	Role          Role      `json:"role"`
	CPUQuotaMilli int       `json:"cpuQuotaMilli"` // 1000 = 1 core
	MemQuotaMB    int       `json:"memQuotaMB"`
	CreatedAt     time.Time `json:"createdAt"`
}

// RequestStatus is the lifecycle state of a resource request.
type RequestStatus string

const (
	StatusPending  RequestStatus = "pending"
	StatusApproved RequestStatus = "approved"
	StatusRejected RequestStatus = "rejected"
)

// ResourceRequest is a member's ask for a CPU/RAM quota, pending admin review.
type ResourceRequest struct {
	ID         int64         `json:"id"`
	UserID     int64         `json:"userId"`
	Username   string        `json:"username"`
	CPUMilli   int           `json:"cpuMilli"`
	MemMB      int           `json:"memMB"`
	Note       string        `json:"note"`
	Status     RequestStatus `json:"status"`
	ReviewedBy string        `json:"reviewedBy"`
	ReviewNote string        `json:"reviewNote"`
	CreatedAt  time.Time     `json:"createdAt"`
	ReviewedAt *time.Time    `json:"reviewedAt"`
}

// --- Users ---

// CountUsers returns the number of accounts (used for first-run bootstrap).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a new account.
func (s *Store) CreateUser(ctx context.Context, u User) (User, error) {
	err := s.pool.QueryRow(ctx, `
INSERT INTO users (username, password_hash, role, cpu_quota_milli, mem_quota_mb)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, created_at`,
		u.Username, u.PasswordHash, u.Role, u.CPUQuotaMilli, u.MemQuotaMB).Scan(&u.ID, &u.CreatedAt)
	if err != nil {
		return User{}, err
	}
	return u, nil
}

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CPUQuotaMilli, &u.MemQuotaMB, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

const userCols = `id, username, password_hash, role, cpu_quota_milli, mem_quota_mb, created_at`

// GetUserByUsername looks up an account by username.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE username = $1`, username))
}

// GetUserByID looks up an account by id.
func (s *Store) GetUserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// ListUsers returns all accounts ordered by username.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUserQuota sets a member's CPU/RAM quota.
func (s *Store) UpdateUserQuota(ctx context.Context, id int64, cpuMilli, memMB int) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET cpu_quota_milli = $1, mem_quota_mb = $2 WHERE id = $3`, cpuMilli, memMB, id)
	return err
}

// DeleteUser removes an account.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

// --- Resource requests ---

// CreateRequest files a new pending resource request.
func (s *Store) CreateRequest(ctx context.Context, r ResourceRequest) (ResourceRequest, error) {
	err := s.pool.QueryRow(ctx, `
INSERT INTO resource_requests (user_id, username, cpu_milli, mem_mb, note, status)
VALUES ($1, $2, $3, $4, $5, 'pending')
RETURNING id, created_at`,
		r.UserID, r.Username, r.CPUMilli, r.MemMB, r.Note).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return ResourceRequest{}, err
	}
	r.Status = StatusPending
	return r, nil
}

const reqCols = `id, user_id, username, cpu_milli, mem_mb, note, status, reviewed_by, review_note, created_at, reviewed_at`

func scanRequest(row pgx.Row) (ResourceRequest, error) {
	var r ResourceRequest
	err := row.Scan(&r.ID, &r.UserID, &r.Username, &r.CPUMilli, &r.MemMB, &r.Note, &r.Status, &r.ReviewedBy, &r.ReviewNote, &r.CreatedAt, &r.ReviewedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResourceRequest{}, ErrNotFound
	}
	return r, err
}

// GetRequest returns one request by id.
func (s *Store) GetRequest(ctx context.Context, id int64) (ResourceRequest, error) {
	return scanRequest(s.pool.QueryRow(ctx, `SELECT `+reqCols+` FROM resource_requests WHERE id = $1`, id))
}

// ListRequests returns requests, optionally filtered by status, newest first.
// When forUser > 0, only that user's requests are returned.
func (s *Store) ListRequests(ctx context.Context, status RequestStatus, forUser int64) ([]ResourceRequest, error) {
	q := `SELECT ` + reqCols + ` FROM resource_requests`
	var args []any
	var conds []string
	if status != "" {
		args = append(args, status)
		conds = append(conds, "status = $"+itoa(len(args)))
	}
	if forUser > 0 {
		args = append(args, forUser)
		conds = append(conds, "user_id = $"+itoa(len(args)))
	}
	for i, c := range conds {
		if i == 0 {
			q += " WHERE "
		} else {
			q += " AND "
		}
		q += c
	}
	q += " ORDER BY created_at DESC"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResourceRequest
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReviewRequest sets a request's outcome and reviewer.
func (s *Store) ReviewRequest(ctx context.Context, id int64, status RequestStatus, reviewedBy, note string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE resource_requests
SET status = $1, reviewed_by = $2, review_note = $3, reviewed_at = now()
WHERE id = $4`, status, reviewedBy, note, id)
	return err
}

// itoa is a tiny helper to avoid importing strconv just for placeholder ids.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
