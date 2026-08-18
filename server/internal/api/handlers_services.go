package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"easydeploy/internal/auth"
	"easydeploy/internal/docker"
	"easydeploy/internal/store"

	"github.com/go-chi/chi/v5"
)

type serviceRequest struct {
	Name          string              `json:"name"`
	Image         string              `json:"image"`
	Subdomain     string              `json:"subdomain"`
	ContainerPort int                 `json:"containerPort"`
	Network       string              `json:"network"`
	Env           []string            `json:"env"`
	CPUMilli      int                 `json:"cpuMilli"`
	MemMB         int                 `json:"memMB"`
	Replicas      int                 `json:"replicas"`
	MinReplicas   int                 `json:"minReplicas"`
	MaxReplicas   int                 `json:"maxReplicas"`
	Autoscale     bool                `json:"autoscale"`
	TargetCPUPct  int                 `json:"targetCpuPercent"`
	GitRepo       string              `json:"gitRepo"`
	GitBranch     string              `json:"gitBranch"`
	GitDockerfile string              `json:"gitDockerfile"`
	Advanced      docker.AdvancedSpec `json:"advanced"`
}

// serviceView augments a stored service with the Host names it is reachable at
// (the auto host plus any custom subdomain) and its parsed advanced options, so
// the edit form can round-trip the full configuration.
type serviceView struct {
	store.Service
	Domains  []string            `json:"domains"`
	Advanced docker.AdvancedSpec `json:"advanced"`
}

func (s *Server) serviceView(r *http.Request, svc store.Service) serviceView {
	var adv docker.AdvancedSpec
	if svc.Advanced != "" {
		_ = json.Unmarshal([]byte(svc.Advanced), &adv)
	}
	return serviceView{
		Service:  svc,
		Domains:  s.registry.ServiceDomains(r.Context(), svc.Name, svc.Subdomain, svc.EndpointID),
		Advanced: adv,
	}
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	owner := ""
	if p := auth.Current(r.Context()); !p.IsAdmin() {
		owner = p.Username
	}
	list, err := s.services.List(r.Context(), owner)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Scope to the selected environment so the Services tab shows only the
	// services that run on the host the user is viewing.
	env := endpointID(r)
	scoped := make([]serviceView, 0, len(list))
	for _, svc := range list {
		if svc.EndpointID == env {
			scoped = append(scoped, s.serviceView(r, svc))
		}
	}
	writeJSON(w, http.StatusOK, scoped)
}

var subdomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func (s *Server) handleSetServiceSubdomain(w http.ResponseWriter, r *http.Request) {
	svc, err := s.ownedService(r)
	if err != nil {
		writeErr(w, statusForOwnershipErr(err), err)
		return
	}
	var req struct {
		Subdomain string `json:"subdomain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sub := strings.ToLower(strings.TrimSpace(req.Subdomain))
	if sub != "" && !subdomainRe.MatchString(sub) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("subdomain must be a single DNS label (a–z, 0–9, hyphen)"))
		return
	}
	if err := s.services.SetSubdomain(r.Context(), svc.Name, sub); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	svc.Subdomain = sub
	writeJSON(w, http.StatusOK, s.serviceView(r, svc))
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	svc, err := s.ownedService(r)
	if err != nil {
		writeErr(w, statusForOwnershipErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.serviceView(r, svc))
}

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	var req serviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" || req.Image == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("name and image are required"))
		return
	}
	req.Replicas = clampMin(req.Replicas, 1)
	req.MinReplicas = clampMin(req.MinReplicas, 1)
	req.MaxReplicas = clampMin(req.MaxReplicas, req.Replicas)
	if req.MaxReplicas < req.MinReplicas {
		req.MaxReplicas = req.MinReplicas
	}
	if req.TargetCPUPct <= 0 {
		req.TargetCPUPct = 70
	}
	if req.GitBranch == "" {
		req.GitBranch = "main"
	}
	if req.GitDockerfile == "" {
		req.GitDockerfile = "Dockerfile"
	}

	// Enforce the member's quota against the maximum this service can consume
	// (its top replica count when autoscaling).
	effReplicas := req.Replicas
	if req.Autoscale && req.MaxReplicas > effReplicas {
		effReplicas = req.MaxReplicas
	}
	owner, err := s.enforceServiceQuota(r, effReplicas, req.CPUMilli, req.MemMB, "")
	if err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}

	envJSON, _ := json.Marshal(req.Env)
	advJSON, _ := json.Marshal(req.Advanced)
	// The service runs on the environment the request targets. Its replicas must
	// share the edge network so that host's Envoy can resolve them by name.
	network := req.Network
	if network == "" {
		network = docker.EdgeNetwork
	}
	svc, err := s.services.Create(r.Context(), store.Service{
		Name:          req.Name,
		Owner:         owner,
		Advanced:      string(advJSON),
		Image:         req.Image,
		Subdomain:     req.Subdomain,
		ContainerPort: req.ContainerPort,
		Network:       network,
		Env:           string(envJSON),
		CPUMilli:      req.CPUMilli,
		MemMB:         req.MemMB,
		Replicas:      req.Replicas,
		MinReplicas:   req.MinReplicas,
		MaxReplicas:   req.MaxReplicas,
		Autoscale:     req.Autoscale,
		TargetCPUPct:  req.TargetCPUPct,
		GitRepo:       req.GitRepo,
		GitBranch:     req.GitBranch,
		GitDockerfile: req.GitDockerfile,
		WebhookToken:  newToken(),
		EndpointID:    endpointID(r),
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.serviceView(r, svc))
}

func (s *Server) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	cur, err := s.ownedService(r)
	if err != nil {
		writeErr(w, statusForOwnershipErr(err), err)
		return
	}
	var req serviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Image == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("image is required"))
		return
	}
	req.Replicas = clampMin(req.Replicas, 1)
	req.MinReplicas = clampMin(req.MinReplicas, 1)
	req.MaxReplicas = clampMin(req.MaxReplicas, req.Replicas)
	if req.MaxReplicas < req.MinReplicas {
		req.MaxReplicas = req.MinReplicas
	}
	if req.TargetCPUPct <= 0 {
		req.TargetCPUPct = 70
	}
	if req.GitBranch == "" {
		req.GitBranch = "main"
	}
	if req.GitDockerfile == "" {
		req.GitDockerfile = "Dockerfile"
	}
	effReplicas := req.Replicas
	if req.Autoscale && req.MaxReplicas > effReplicas {
		effReplicas = req.MaxReplicas
	}
	// Enforce quota, excluding this service's own (about-to-be-replaced)
	// replicas so an unchanged edit isn't rejected against itself.
	if _, err := s.enforceServiceQuota(r, effReplicas, req.CPUMilli, req.MemMB, cur.Name); err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}

	envJSON, _ := json.Marshal(req.Env)
	advJSON, _ := json.Marshal(req.Advanced)
	network := req.Network
	if network == "" {
		network = docker.EdgeNetwork
	}
	updated, err := s.services.Update(r.Context(), cur.Name, store.Service{
		Image:         req.Image,
		Subdomain:     req.Subdomain,
		ContainerPort: req.ContainerPort,
		Network:       network,
		Env:           string(envJSON),
		CPUMilli:      req.CPUMilli,
		MemMB:         req.MemMB,
		Replicas:      req.Replicas,
		MinReplicas:   req.MinReplicas,
		MaxReplicas:   req.MaxReplicas,
		Autoscale:     req.Autoscale,
		TargetCPUPct:  req.TargetCPUPct,
		GitRepo:       req.GitRepo,
		GitBranch:     req.GitBranch,
		GitDockerfile: req.GitDockerfile,
		Advanced:      string(advJSON),
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, s.serviceView(r, updated))
}

func (s *Server) handleScaleService(w http.ResponseWriter, r *http.Request) {
	if _, err := s.ownedService(r); err != nil {
		writeErr(w, statusForOwnershipErr(err), err)
		return
	}
	var req struct {
		Replicas int `json:"replicas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.services.Scale(r.Context(), chi.URLParam(r, "name"), req.Replicas); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "scaled", "replicas": req.Replicas})
}

func (s *Server) handleRedeployService(w http.ResponseWriter, r *http.Request) {
	svc, err := s.ownedService(r)
	if err != nil {
		writeErr(w, statusForOwnershipErr(err), err)
		return
	}
	var req struct {
		Image string `json:"image"` // optional
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// If the service is git-backed and no explicit image is given, build from
	// the repo; otherwise re-pull/replace the image. Build runs in the
	// background because clone+build can take a while.
	if svc.GitRepo != "" && req.Image == "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if err := s.services.BuildAndRedeploy(ctx, svc); err != nil {
				// Logged inside; nothing to return to the caller.
				_ = err
			}
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "building"})
		return
	}
	if err := s.services.Redeploy(r.Context(), svc.Name, req.Image); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "redeployed"})
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	if _, err := s.ownedService(r); err != nil {
		writeErr(w, statusForOwnershipErr(err), err)
		return
	}
	if err := s.services.Delete(r.Context(), chi.URLParam(r, "name")); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleWebhook is the public git webhook. It authenticates by the token in
// the path, acknowledges immediately, and rebuilds+redeploys in the
// background.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	svc, err := s.store.GetServiceByToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("unknown webhook"))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := s.services.BuildAndRedeploy(ctx, svc); err != nil {
			_ = err // logged in the manager
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "service": svc.Name})
}

// --- helpers ---

// ownedService loads the {name} service and checks the caller may manage it.
func (s *Server) ownedService(r *http.Request) (store.Service, error) {
	svc, err := s.services.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return store.Service{}, err
	}
	if p := auth.Current(r.Context()); !p.IsAdmin() && svc.Owner != p.Username {
		return store.Service{}, errForbidden
	}
	return svc, nil
}

// enforceServiceQuota checks a member has room for replicas*limits and returns
// the owner username to stamp on the service.
// enforceServiceQuota validates a member's request against their quota, counting
// their live usage on the *target* environment (so a member's footprint is
// bounded per host they can deploy to). excludeService, when set, omits that
// service's current replicas from the total (used on edit).
func (s *Server) enforceServiceQuota(r *http.Request, replicas, cpuMilli, memMB int, excludeService string) (string, error) {
	ctx := r.Context()
	p := auth.Current(ctx)
	user, err := s.store.GetUserByID(ctx, p.UserID)
	if err != nil {
		return "", err
	}
	if user.Role == store.RoleAdmin {
		return user.Username, nil
	}
	// Quota is per environment: the local host uses the user's base quota; a
	// remote host uses the quota granted on that host.
	env := endpointID(r)
	quotaCPU, quotaMem := user.CPUQuotaMilli, user.MemQuotaMB
	if env != 0 {
		g, ok, err := s.store.GetUserEndpointQuota(ctx, user.ID, env)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("no access to this environment")
		}
		quotaCPU, quotaMem = g.CPUQuotaMilli, g.MemQuotaMB
	}
	if quotaCPU <= 0 || quotaMem <= 0 {
		return "", fmt.Errorf("no resource quota granted yet on this environment — submit a request and wait for admin approval")
	}
	if cpuMilli <= 0 || memMB <= 0 {
		return "", fmt.Errorf("cpu (millicores) and memory (MB) per replica are required")
	}
	cli, err := s.clientFor(r)
	if err != nil {
		return "", err
	}
	usedNano, usedMem, err := cli.OwnerUsageExcludingService(ctx, user.Username, excludeService)
	if err != nil {
		return "", err
	}
	needCPU := usedNano/nanoPerMilliCPU + int64(replicas*cpuMilli)
	needMem := usedMem/bytesPerMB + int64(replicas*memMB)
	if needCPU > int64(quotaCPU) || needMem > int64(quotaMem) {
		return "", fmt.Errorf(
			"exceeds quota: %d replicas x %dm/%dMB plus current use exceeds quota %dm/%dMB",
			replicas, cpuMilli, memMB, quotaCPU, quotaMem)
	}
	return user.Username, nil
}

func newToken() string {
	buf := make([]byte, 20)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func clampMin(v, min int) int {
	if v < min {
		return min
	}
	return v
}
