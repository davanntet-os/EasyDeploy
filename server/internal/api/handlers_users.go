package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"easydeploy/internal/auth"
	"easydeploy/internal/docker"
	"easydeploy/internal/store"

	"github.com/go-chi/chi/v5"
)

// requireContainerOwner is middleware that, for non-admins, verifies the
// container named by the {id} URL param is labeled with the caller's username.
// Admins pass through unchecked.
func (s *Server) requireContainerOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.Current(r.Context())
		if p.IsAdmin() {
			next.ServeHTTP(w, r)
			return
		}
		cli, err := s.clientFor(r)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		info, err := cli.Inspect(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		owner := ""
		if info.Config != nil {
			owner = info.Config.Labels[docker.LabelOwner]
		}
		if owner != p.Username {
			writeErr(w, http.StatusForbidden, fmt.Errorf("you do not own this container"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ownerScope returns the username a member's list/create should be scoped to,
// or "" for an admin (who sees and owns everything, unscoped).
func ownerScope(r *http.Request) string {
	p := auth.Current(r.Context())
	if p.IsAdmin() {
		return ""
	}
	return p.Username
}

// requireVolumeOwner is middleware that, for non-admins, verifies the volume
// named by the {name} URL param is labeled with the caller's username. Admins
// pass through unchecked.
func (s *Server) requireVolumeOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.Current(r.Context())
		if p.IsAdmin() {
			next.ServeHTTP(w, r)
			return
		}
		cli, err := s.clientFor(r)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		v, err := cli.InspectVolume(r.Context(), chi.URLParam(r, "name"))
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		if v.Labels[docker.LabelOwner] != p.Username {
			writeErr(w, http.StatusForbidden, fmt.Errorf("you do not own this volume"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireNetworkOwner is middleware that, for non-admins, verifies the network
// identified by the {id} URL param is labeled with the caller's username.
// Admins pass through unchecked.
func (s *Server) requireNetworkOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.Current(r.Context())
		if p.IsAdmin() {
			next.ServeHTTP(w, r)
			return
		}
		cli, err := s.clientFor(r)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		info, err := cli.InspectNetwork(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		if info.Labels[docker.LabelOwner] != p.Username {
			writeErr(w, http.StatusForbidden, fmt.Errorf("you do not own this network"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireRegistryOwner is middleware that, for non-admins, verifies the registry
// identified by the {id} URL param is owned by the caller. Admins pass through.
func (s *Server) requireRegistryOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.Current(r.Context())
		if p.IsAdmin() {
			next.ServeHTTP(w, r)
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		owner, err := s.registries.Owner(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		if owner != p.Username {
			writeErr(w, http.StatusForbidden, fmt.Errorf("you do not own this registry"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

const (
	nanoPerMilliCPU = 1_000_000 // 1000 milli = 1 core = 1e9 nano
	bytesPerMB      = 1 << 20
)

// handleMe returns the current user with their live resource usage.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := auth.Current(r.Context())
	user, err := s.store.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	// Quota and usage are per environment: the local host uses the user's base
	// quota; a remote host uses the quota granted there. The client sends the
	// selected environment via X-Endpoint-Id, so switching hosts updates this.
	env := endpointID(r)
	cpuQ, memQ, diskQ := user.CPUQuotaMilli, user.MemQuotaMB, user.DiskQuotaMB
	if env != 0 {
		cpuQ, memQ, diskQ = 0, 0, 0
		if g, ok, _ := s.store.GetUserEndpointQuota(r.Context(), user.ID, env); ok {
			cpuQ, memQ, diskQ = g.CPUQuotaMilli, g.MemQuotaMB, g.DiskQuotaMB
		}
	}
	// Live CPU/RAM usage is counted on the selected host.
	var usedNano, usedMem int64
	if cli, err := s.clientFor(r); err == nil {
		usedNano, usedMem, _ = cli.OwnerUsage(r.Context(), user.Username)
	}
	diskUsed, _ := s.store.OwnerDiskUsageMB(r.Context(), env, user.Username, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"username":      user.Username,
		"role":          user.Role,
		"cpuQuotaMilli": cpuQ,
		"memQuotaMB":    memQ,
		"diskQuotaMB":   diskQ,
		"cpuUsedMilli":  usedNano / nanoPerMilliCPU,
		"memUsedMB":     usedMem / bytesPerMB,
		"diskUsedMB":    diskUsed,
	})
}

// --- users (admin only) ---

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username      string     `json:"username"`
		Password      string     `json:"password"`
		Role          store.Role `json:"role"`
		CPUQuotaMilli int        `json:"cpuQuotaMilli"`
		MemQuotaMB    int        `json:"memQuotaMB"`
		DiskQuotaMB   int        `json:"diskQuotaMB"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Username == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("username and password are required"))
		return
	}
	if req.Role != store.RoleAdmin {
		req.Role = store.RoleMember
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	user, err := s.store.CreateUser(r.Context(), store.User{
		Username:      req.Username,
		PasswordHash:  hash,
		Role:          req.Role,
		CPUQuotaMilli: req.CPUQuotaMilli,
		MemQuotaMB:    req.MemQuotaMB,
		DiskQuotaMB:   req.DiskQuotaMB,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleUpdateUserQuota(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		CPUQuotaMilli int `json:"cpuQuotaMilli"`
		MemQuotaMB    int `json:"memQuotaMB"`
		DiskQuotaMB   int `json:"diskQuotaMB"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.UpdateUserQuota(r.Context(), id, req.CPUQuotaMilli, req.MemQuotaMB, req.DiskQuotaMB); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleSetUserRole changes a user's role. It refuses to demote the last admin
// so the instance can't be locked out.
func (s *Server) handleSetUserRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	role := store.Role(req.Role)
	if role != store.RoleAdmin && role != store.RoleMember {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("role must be admin or member"))
		return
	}
	if role == store.RoleMember {
		if u, err := s.store.GetUserByID(r.Context(), id); err == nil && u.Role == store.RoleAdmin {
			if n, err := s.store.CountAdmins(r.Context()); err == nil && n <= 1 {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("cannot demote the last admin"))
				return
			}
		}
	}
	if err := s.store.SetUserRole(r.Context(), id, role); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleResetPassword sets a new password for a user (admin only).
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.Password) < 6 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("password must be at least 6 characters"))
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.store.SetUserPassword(r.Context(), id, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if id == auth.Current(r.Context()).UserID {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("cannot delete your own account"))
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- resource requests ---

// handleCreateRequest lets a member file a CPU/RAM quota request.
func (s *Server) handleCreateRequest(w http.ResponseWriter, r *http.Request) {
	p := auth.Current(r.Context())
	var req struct {
		EndpointID int64  `json:"endpointId"`
		CPUMilli   int    `json:"cpuMilli"`
		MemMB      int    `json:"memMB"`
		DiskMB     int    `json:"diskMB"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.CPUMilli <= 0 && req.MemMB <= 0 && req.DiskMB <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("request at least one of CPU, memory, or storage"))
		return
	}
	// A member may only request quota for the local host or an environment they
	// already have access to (access itself is granted by an admin).
	if req.EndpointID != 0 && !p.IsAdmin() {
		ok, err := s.store.UserHasEndpoint(r.Context(), p.UserID, req.EndpointID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			writeErr(w, http.StatusForbidden, fmt.Errorf("you don't have access to that environment"))
			return
		}
	}
	created, err := s.store.CreateRequest(r.Context(), store.ResourceRequest{
		UserID:     p.UserID,
		Username:   p.Username,
		EndpointID: req.EndpointID,
		CPUMilli:   req.CPUMilli,
		MemMB:      req.MemMB,
		DiskMB:     req.DiskMB,
		Note:       req.Note,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleListRequests returns all requests for admins, or the caller's own.
func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	p := auth.Current(r.Context())
	status := store.RequestStatus(r.URL.Query().Get("status"))
	var forUser int64
	if !p.IsAdmin() {
		forUser = p.UserID
	}
	list, err := s.store.ListRequests(r.Context(), status, forUser)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleReviewRequest approves or rejects a request. Approving grants the
// requesting member the CPU/RAM quota (admins may override the granted
// amounts), which they then deploy against.
func (s *Server) handleReviewRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Approve       bool   `json:"approve"`
		GrantCPUMilli int    `json:"grantCpuMilli"` // optional override
		GrantMemMB    int    `json:"grantMemMB"`    // optional override
		GrantDiskMB   int    `json:"grantDiskMB"`   // optional override
		Note          string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rr, err := s.store.GetRequest(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	reviewer := auth.Current(r.Context()).Username
	if !req.Approve {
		if err := s.store.ReviewRequest(r.Context(), id, store.StatusRejected, reviewer, req.Note); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
		return
	}
	// Approve: set the targeted environment's quota. Each dimension is the admin
	// override if given, else the requested amount, else the user's current
	// value — so approving a storage-only request keeps their CPU/RAM intact.
	var curCPU, curMem, curDisk int
	if rr.EndpointID == 0 {
		if u, err := s.store.GetUserByID(r.Context(), rr.UserID); err == nil {
			curCPU, curMem, curDisk = u.CPUQuotaMilli, u.MemQuotaMB, u.DiskQuotaMB
		}
	} else {
		if g, ok, _ := s.store.GetUserEndpointQuota(r.Context(), rr.UserID, rr.EndpointID); ok {
			curCPU, curMem, curDisk = g.CPUQuotaMilli, g.MemQuotaMB, g.DiskQuotaMB
		}
	}
	pick := func(override, requested, current int) int {
		if override > 0 {
			return override
		}
		if requested > 0 {
			return requested
		}
		return current
	}
	grantCPU := pick(req.GrantCPUMilli, rr.CPUMilli, curCPU)
	grantMem := pick(req.GrantMemMB, rr.MemMB, curMem)
	grantDisk := pick(req.GrantDiskMB, rr.DiskMB, curDisk)
	// Grant on the environment the request targeted: the local host writes the
	// user's base quota; a remote grants (or updates) the per-env quota, which
	// also grants access to that host.
	if rr.EndpointID == 0 {
		err = s.store.UpdateUserQuota(r.Context(), rr.UserID, grantCPU, grantMem, grantDisk)
	} else {
		err = s.store.GrantUserEndpointQuota(r.Context(), rr.UserID, rr.EndpointID, grantCPU, grantMem, grantDisk)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.store.ReviewRequest(r.Context(), id, store.StatusApproved, reviewer, req.Note); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "approved", "grantedCpuMilli": grantCPU, "grantedMemMB": grantMem, "grantedDiskMB": grantDisk})
}

// --- quota enforcement (used by deploy/edit) ---

// resolveResources validates and returns the Docker limits for a deploy or
// edit, enforcing the member's quota. Admins bypass quota but may still set
// limits. reqCPUMilli/reqMemMB are the requested per-container limits.
// excludeID, when non-empty, is a container whose current limits are
// subtracted from usage (used on edit, where the container is being replaced).
func (s *Server) resolveResources(ctx context.Context, excludeID string, reqCPUMilli, reqMemMB int) (owner string, nanoCPUs, memBytes int64, err error) {
	p := auth.Current(ctx)
	user, err := s.store.GetUserByID(ctx, p.UserID)
	if err != nil {
		return "", 0, 0, err
	}
	nanoCPUs = int64(reqCPUMilli) * nanoPerMilliCPU
	memBytes = int64(reqMemMB) * bytesPerMB

	if user.Role == store.RoleAdmin {
		// Limits optional for admins; owner still recorded.
		return user.Username, nanoCPUs, memBytes, nil
	}

	// Members must have an approved quota and specify limits within it.
	if user.CPUQuotaMilli <= 0 || user.MemQuotaMB <= 0 {
		return "", 0, 0, fmt.Errorf("no resource quota granted yet — submit a request and wait for admin approval")
	}
	if reqCPUMilli <= 0 || reqMemMB <= 0 {
		return "", 0, 0, fmt.Errorf("cpu (millicores) and memory (MB) are required")
	}
	usedNano, usedMem, err := s.docker.OwnerUsage(ctx, user.Username)
	if err != nil {
		return "", 0, 0, err
	}
	// Don't count the container being replaced against the new request.
	if excludeID != "" {
		if info, ierr := s.docker.Inspect(ctx, excludeID); ierr == nil && info.HostConfig != nil {
			usedNano -= info.HostConfig.NanoCPUs
			usedMem -= info.HostConfig.Memory
		}
	}
	if usedNano < 0 {
		usedNano = 0
	}
	if usedMem < 0 {
		usedMem = 0
	}
	newCPU := usedNano/nanoPerMilliCPU + int64(reqCPUMilli)
	newMem := usedMem/bytesPerMB + int64(reqMemMB)
	if newCPU > int64(user.CPUQuotaMilli) || newMem > int64(user.MemQuotaMB) {
		return "", 0, 0, fmt.Errorf(
			"exceeds quota: in use %dm CPU / %dMB, quota %dm / %dMB, requested %dm / %dMB",
			usedNano/nanoPerMilliCPU, usedMem/bytesPerMB, user.CPUQuotaMilli, user.MemQuotaMB, reqCPUMilli, reqMemMB)
	}
	return user.Username, nanoCPUs, memBytes, nil
}
