package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"easydeploy/internal/auth"
	"easydeploy/internal/docker"
	"easydeploy/internal/store"
	"easydeploy/internal/tunnel"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	err := s.docker.Ping(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          err == nil,
		"dockerError": errString(err),
	})
}

// --- containers ---

func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	cli, err := s.clientFor(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	all := r.URL.Query().Get("all") != "false"
	list, err := cli.ListContainers(r.Context(), all)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// Members only see the containers they own; admins see everything.
	if p := auth.Current(r.Context()); !p.IsAdmin() {
		owned := list[:0]
		for _, c := range list {
			if c.Labels[docker.LabelOwner] == p.Username {
				owned = append(owned, c)
			}
		}
		list = owned
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleInspectContainer(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, info)
}

// handleContainerAction adapts a docker client method into an HTTP handler,
// targeting the request's selected environment.
func (s *Server) handleContainerAction(action func(*docker.Client, context.Context, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cli, err := s.clientFor(r)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		if err := action(cli, r.Context(), chi.URLParam(r, "id")); err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	cli, err := s.clientFor(r)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	id := chi.URLParam(r, "id")
	logs, err := cli.Logs(r.Context(), id, true)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	defer logs.Close()
	closeOnDisconnect(r.Context(), conn, logs)
	writer := &wsWriter{conn: conn}
	// Docker multiplexes stdout/stderr in one stream; demux into text frames.
	_, _ = stdcopy.StdCopy(writer, writer, logs)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	cli, err := s.clientFor(r)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	id := chi.URLParam(r, "id")
	stats, err := cli.Stats(r.Context(), id, true)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	defer stats.Close()
	closeOnDisconnect(r.Context(), conn, stats)
	dec := json.NewDecoder(stats)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
			return
		}
	}
}

// handleExec bridges a WebSocket to an interactive shell (docker exec with a
// TTY) inside the container. Client -> server binary frames are stdin; text
// frames are JSON resize control ({"cols":N,"rows":N}). Server -> client
// binary frames are the raw terminal output.
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	shell := r.URL.Query().Get("shell")
	if shell == "" {
		shell = "/bin/sh"
	}
	cli, err := s.clientFor(r)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	id := chi.URLParam(r, "id")
	hijack, execID, err := cli.Exec(r.Context(), id, []string{shell})
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("exec failed: "+err.Error()))
		return
	}
	defer hijack.Close()

	// Pump container output to the browser. When the shell exits (EOF), close
	// the socket so the client's read loop below unblocks and the browser sees
	// the session end.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := hijack.Reader.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\n[session ended]\r\n"))
				_ = conn.Close()
				return
			}
		}
	}()

	// Pump browser input (keystrokes) and resize control to the container.
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch mt {
		case websocket.TextMessage:
			var resize struct {
				Cols uint `json:"cols"`
				Rows uint `json:"rows"`
			}
			if json.Unmarshal(data, &resize) == nil && resize.Cols > 0 {
				_ = cli.ExecResize(r.Context(), execID, resize.Rows, resize.Cols)
			}
		case websocket.BinaryMessage:
			if _, err := hijack.Conn.Write(data); err != nil {
				return
			}
		}
	}
}

// --- images / volumes / networks ---

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	cli, err := s.clientFor(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	list, err := cli.ListImages(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleListVolumes(w http.ResponseWriter, r *http.Request) {
	cli, err := s.clientFor(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	list, err := cli.ListVolumes(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// Size/ref-count are NOT computed here — DiskUsage is slow on busy hosts.
	// The client fetches them lazily via /volumes/usage.
	type volumeView struct {
		Name       string            `json:"name"`
		Driver     string            `json:"driver"`
		Mountpoint string            `json:"mountpoint"`
		CreatedAt  string            `json:"createdAt"`
		Labels     map[string]string `json:"labels"`
		Size       int64             `json:"size"`     // -1 = not yet computed
		RefCount   int64             `json:"refCount"` // -1 = not yet computed
	}
	owner := ownerScope(r) // "" for admins = all; members see only their own
	out := make([]volumeView, 0, len(list))
	for _, v := range list {
		if owner != "" && v.Labels[docker.LabelOwner] != owner {
			continue
		}
		out = append(out, volumeView{
			Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint,
			CreatedAt: v.CreatedAt, Labels: v.Labels, Size: -1, RefCount: -1,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleVolumeUsage returns per-volume size + ref-count (the slow DiskUsage
// call), fetched lazily by the client so the volume list itself is instant.
func (s *Server) handleVolumeUsage(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	usage, err := cli.VolumeUsage(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// For members, restrict usage to volumes they own so sizes of others'
	// volumes never leak.
	var owned map[string]bool
	if owner := ownerScope(r); owner != "" {
		vols, err := cli.ListVolumes(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		owned = make(map[string]bool)
		for _, v := range vols {
			if v.Labels[docker.LabelOwner] == owner {
				owned[v.Name] = true
			}
		}
	}
	type u struct {
		Size     int64 `json:"size"`
		RefCount int64 `json:"refCount"`
	}
	out := make(map[string]u, len(usage))
	for name, ud := range usage {
		if owned != nil && !owned[name] {
			continue
		}
		out[name] = u{Size: ud.Size, RefCount: ud.RefCount}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleVolumeNames returns just the volume names on the selected host — a
// lightweight, non-admin list so the service form can offer existing volumes as
// mount sources.
func (s *Server) handleVolumeNames(w http.ResponseWriter, r *http.Request) {
	cli, ok := s.dockerOr502(w, r)
	if !ok {
		return
	}
	vols, err := cli.ListVolumes(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	owner := ownerScope(r)
	names := make([]string, 0, len(vols))
	for _, v := range vols {
		if owner != "" && v.Labels[docker.LabelOwner] != owner {
			continue
		}
		names = append(names, v.Name)
	}
	writeJSON(w, http.StatusOK, names)
}

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	cli, err := s.clientFor(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	list, err := cli.ListNetworks(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// Members see only networks they own; admins see all.
	if owner := ownerScope(r); owner != "" {
		scoped := list[:0:0]
		for _, n := range list {
			if n.Labels[docker.LabelOwner] == owner {
				scoped = append(scoped, n)
			}
		}
		list = scoped
	}
	writeJSON(w, http.StatusOK, list)
}

// --- deploy ---

type deployRequest struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Env           []string          `json:"env"`
	Subdomain     string            `json:"subdomain"`
	ContainerPort int               `json:"containerPort"`
	Publish       map[string]string `json:"publish"`
	Network       string            `json:"network"`
	CPUMilli      int               `json:"cpuMilli"` // per-container CPU cap (1000 = 1 core)
	MemMB         int               `json:"memMB"`    // per-container memory cap
}

// --- routes ---

func (s *Server) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	list, err := s.registry.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Scope to the selected environment (routes are per-host).
	env := endpointID(r)
	scoped := make([]store.Route, 0, len(list))
	for _, rt := range list {
		if rt.EndpointID == env {
			scoped = append(scoped, rt)
		}
	}
	writeJSON(w, http.StatusOK, scoped)
}

func (s *Server) handleUpsertRoute(w http.ResponseWriter, r *http.Request) {
	var route store.Route
	if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	route.EndpointID = endpointID(r) // the route is served on the selected host
	saved, err := s.registry.Upsert(r.Context(), route)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	if err := s.registry.Delete(r.Context(), chi.URLParam(r, "subdomain")); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- tunnels ---

func (s *Server) handlePublicIP(w http.ResponseWriter, r *http.Request) {
	ip, err := tunnel.PublicIP(r.Context(), s.cfg.PublicIPService)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ip": ip})
}

func (s *Server) handleListTunnels(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListTunnels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Annotate with live running state.
	type tunnelView struct {
		store.Tunnel
		Running bool `json:"running"`
	}
	out := make([]tunnelView, 0, len(list))
	for _, t := range list {
		out = append(out, tunnelView{Tunnel: t, Running: s.tunnels.IsRunning(t.ID)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	var t store.Tunnel
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	saved, err := s.store.InsertTunnel(r.Context(), t)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) handleStartTunnel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	tunnels, err := s.store.ListTunnels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var found *store.Tunnel
	for i := range tunnels {
		if tunnels[i].ID == id {
			found = &tunnels[i]
			break
		}
	}
	if found == nil {
		writeErr(w, http.StatusNotFound, errNotFound)
		return
	}
	// Start against a background context so the tunnel outlives the request.
	if err := s.tunnels.Start(context.Background(), *found); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	_ = s.store.SetTunnelEnabled(r.Context(), id, true)
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.tunnels.Stop(id)
	_ = s.store.SetTunnelEnabled(r.Context(), id, false)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.tunnels.Stop(id)
	if err := s.store.DeleteTunnel(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- websocket plumbing ---

// wsWriter adapts an io.Writer onto a WebSocket connection, emitting each
// write as a text frame. Writes are serialized because the gorilla
// connection is not safe for concurrent writers (stdout+stderr demux).
type wsWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.TextMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// closeOnDisconnect closes the docker stream when the client disconnects or
// the request context is cancelled, unblocking the copy loop.
func closeOnDisconnect(ctx context.Context, conn *websocket.Conn, stream interface{ Close() error }) {
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				_ = stream.Close()
				return
			}
		}
	}()
	go func() {
		<-ctx.Done()
		_ = stream.Close()
	}()
}
