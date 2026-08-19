package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"easydeploy/internal/docker"
	"easydeploy/internal/store"

	"github.com/go-chi/chi/v5"
)

// --- auth ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	token, user, err := s.auth.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  map[string]any{"username": user.Username, "role": user.Role},
	})
}

// --- container edit (reconfigure) ---

func (s *Server) handleEditContainer(w http.ResponseWriter, r *http.Request) {
	var req deployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id := chi.URLParam(r, "id")
	cli, err := s.clientFor(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	local := endpointID(r) == 0

	// Read the previous subdomain so we can retire its route if it changed.
	var oldSubdomain string
	if info, ierr := cli.Inspect(r.Context(), id); ierr == nil && info.Config != nil {
		oldSubdomain = info.Config.Labels["easydeploy.subdomain"]
	}

	// Enforce quota (members), excluding this container's current allocation
	// since it is being replaced.
	owner, nanoCPUs, memBytes, err := s.resolveResources(r.Context(), id, req.CPUMilli, req.MemMB)
	if err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}

	regAuth, _ := s.registries.AuthForImage(r.Context(), req.Image)
	newID, err := cli.EditContainer(r.Context(), id, docker.DeploySpec{
		Name:          req.Name,
		Image:         req.Image,
		Env:           req.Env,
		Subdomain:     req.Subdomain,
		ContainerPort: req.ContainerPort,
		Publish:       req.Publish,
		Network:       req.Network,
		RegistryAuth:  regAuth,
		Owner:         owner,
		NanoCPUs:      nanoCPUs,
		MemoryBytes:   memBytes,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	// Reconcile the proxy route only for the local host — Envoy can't reach
	// containers on remote environments by name.
	if local {
		if oldSubdomain != "" && oldSubdomain != req.Subdomain {
			_ = s.registry.Delete(r.Context(), oldSubdomain)
		}
		if req.Subdomain != "" && req.ContainerPort > 0 {
			_, _ = s.registry.Upsert(r.Context(), store.Route{
				Subdomain:   req.Subdomain,
				ContainerID: newID,
				TargetHost:  req.Name,
				TargetPort:  req.ContainerPort,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"containerId": newID})
}

// --- container update ---

func (s *Server) handleUpdateContainer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"image"` // optional; empty re-pulls current image
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	cli, err := s.clientFor(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	// Determine the image so we can attach registry auth for private pulls.
	image := req.Image
	if image == "" {
		if info, ierr := cli.Inspect(r.Context(), chi.URLParam(r, "id")); ierr == nil && info.Config != nil {
			image = info.Config.Image
		}
	}
	auth, _ := s.registries.AuthForImage(r.Context(), image)

	newID, err := cli.Recreate(r.Context(), chi.URLParam(r, "id"), req.Image, auth)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"containerId": newID})
}

// --- images ---

func (s *Server) handleRemoveImage(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if err := cli.RemoveImage(r.Context(), chi.URLParam(r, "id"), force); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// --- networks ---

func (s *Server) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	var req struct {
		Name   string `json:"name"`
		Driver string `json:"driver"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Stamp the creator as owner so members manage only their own networks.
	labels := map[string]string{docker.LabelManaged: "true"}
	if owner := ownerScope(r); owner != "" {
		labels[docker.LabelOwner] = owner
	}
	id, err := cli.CreateNetwork(r.Context(), req.Name, req.Driver, labels)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) handleInspectNetwork(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	info, err := cli.InspectNetwork(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleRemoveNetwork(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	if err := cli.RemoveNetwork(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleConnectNetwork(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	var req struct {
		ContainerID string `json:"containerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := cli.ConnectNetwork(r.Context(), chi.URLParam(r, "id"), req.ContainerID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
}

func (s *Server) handleDisconnectNetwork(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	var req struct {
		ContainerID string `json:"containerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := cli.DisconnectNetwork(r.Context(), chi.URLParam(r, "id"), req.ContainerID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// --- volumes ---

func (s *Server) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	var req struct {
		Name   string            `json:"name"`
		Driver string            `json:"driver"`
		Labels map[string]string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Stamp the creator as owner so members manage only their own volumes.
	if owner := ownerScope(r); owner != "" {
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		req.Labels[docker.LabelOwner] = owner
	}
	v, err := cli.CreateVolume(r.Context(), req.Name, req.Driver, req.Labels)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) handleInspectVolume(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	v, err := cli.InspectVolume(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleRemoveVolume(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if err := cli.RemoveVolume(r.Context(), chi.URLParam(r, "name"), force); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleBrowseVolume(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	files, err := cli.BrowseVolume(r.Context(), chi.URLParam(r, "name"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) handleMkdirVolume(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := cli.MkdirVolume(r.Context(), chi.URLParam(r, "name"), req.Path); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "created"})
}

func (s *Server) handleDeleteVolumeFile(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	if err := cli.DeleteInVolume(r.Context(), chi.URLParam(r, "name"), r.URL.Query().Get("path")); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleUploadVolume(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("missing 'file' field"))
		return
	}
	defer file.Close()
	dir := r.URL.Query().Get("path")
	if err := cli.UploadToVolume(r.Context(), chi.URLParam(r, "name"), dir, header.Filename, file, header.Size); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "uploaded", "name": header.Filename})
}

func (s *Server) handleDownloadVolume(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	dl, err := cli.DownloadFromVolume(r.Context(), chi.URLParam(r, "name"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	defer dl.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", dl.Name))
	if dl.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(dl.Size, 10))
	}
	_, _ = io.Copy(w, dl)
}

// --- registries ---

func (s *Server) handleListRegistries(w http.ResponseWriter, r *http.Request) {
	// Members see only their own registries; admins see all.
	list, err := s.registries.List(r.Context(), ownerScope(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateRegistry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	reg, err := s.registries.Create(r.Context(), req.Name, req.URL, req.Username, req.Password, ownerScope(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, reg)
}

func (s *Server) handleTestRegistry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.registries.TestLogin(r.Context(), req.URL, req.Username, req.Password); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteRegistry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.registries.Delete(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleRegistryCatalog(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repos, err := s.registries.Catalog(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
}

func (s *Server) handleRegistryTags(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		writeErr(w, http.StatusBadRequest, errNotFound)
		return
	}
	tags, err := s.registries.Tags(r.Context(), id, repo)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}
