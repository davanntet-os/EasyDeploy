package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"easydeploy/internal/docker"
	"easydeploy/internal/endpoint"
	"easydeploy/internal/xds"

	"github.com/go-chi/chi/v5"
)

// endpointView is the client-facing shape of an environment (no secrets).
type endpointView struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Host  string `json:"host"`
	TLS   bool   `json:"tls"`
	Local bool   `json:"local"`
}

// handleListEndpoints returns the local host plus all remote environments.
func (s *Server) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	remotes, err := s.endpoints.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := []endpointView{{ID: endpoint.LocalID, Name: "Local", Host: "local socket", Local: true}}
	for _, e := range remotes {
		out = append(out, endpointView{ID: e.ID, Name: e.Name, Host: e.Host, TLS: e.TLS})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Host   string `json:"host"`
		TLSCA  string `json:"tlsCa"`
		TLSCrt string `json:"tlsCert"`
		TLSKey string `json:"tlsKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ep, err := s.endpoints.Create(r.Context(), req.Name, req.Host, req.TLSCA, req.TLSCrt, req.TLSKey)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, endpointView{ID: ep.ID, Name: ep.Name, Host: ep.Host, TLS: ep.TLS})
}

func (s *Server) handleEndpointStatus(w http.ResponseWriter, r *http.Request) {
	id := endpointIDParam(r)
	ok, version := s.endpoints.Status(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "version": version})
}

func (s *Server) handleDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := endpointIDParam(r)
	if id == endpoint.LocalID {
		writeErr(w, http.StatusBadRequest, errCannotDeleteLocal)
		return
	}
	if err := s.endpoints.Delete(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func endpointIDParam(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}

// --- edge proxy (per-host Envoy so Routes/Services work on a remote host) ---

// handleEdgeStatus reports whether a remote environment's edge Envoy is running.
func (s *Server) handleEdgeStatus(w http.ResponseWriter, r *http.Request) {
	id := endpointIDParam(r)
	cli, err := s.endpoints.ClientFor(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	st, err := cli.EdgeStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleDeployEdge (re)deploys the edge Envoy on a remote host and points it at
// this control plane. The local host (id 0) already has its own Envoy, so this
// is remote-only.
func (s *Server) handleDeployEdge(w http.ResponseWriter, r *http.Request) {
	id := endpointIDParam(r)
	if id == endpoint.LocalID {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("the local host runs its own Envoy; edge proxies are for remote environments"))
		return
	}
	if s.cfg.XDSAdvertiseAddr == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("set EASYDEPLOY_XDS_ADVERTISE_ADDR (this machine's reachable host:port for the xDS port) before deploying a remote edge proxy"))
		return
	}
	var req struct {
		HostPort int `json:"hostPort"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body optional

	nodeID := s.xds.NodeForEndpoint(id)
	bootstrap, err := xds.EdgeBootstrapYAML(nodeID, s.cfg.XDSAdvertiseAddr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cli, err := s.endpoints.ClientFor(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if err := cli.DeployEdge(r.Context(), docker.EdgeSpec{
		NodeID: nodeID, BootstrapYAML: bootstrap, HostPort: req.HostPort,
	}); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// Push this environment's current routes/services to the freshly-connected
	// edge so it serves traffic immediately.
	if err := s.registry.Sync(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	st, err := cli.EdgeStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleRemoveEdge tears down a remote environment's edge Envoy.
func (s *Server) handleRemoveEdge(w http.ResponseWriter, r *http.Request) {
	id := endpointIDParam(r)
	cli, err := s.endpoints.ClientFor(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if err := cli.RemoveEdge(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
