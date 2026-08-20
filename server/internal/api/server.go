// Package api exposes EasyDeploy's REST + WebSocket surface and wires the
// HTTP router to the docker, proxy, and tunnel subsystems.
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"easydeploy/internal/auth"
	"easydeploy/internal/config"
	"easydeploy/internal/docker"
	"easydeploy/internal/endpoint"
	"easydeploy/internal/proxy"
	"easydeploy/internal/registry"
	"easydeploy/internal/service"
	"easydeploy/internal/store"
	"easydeploy/internal/tunnel"
	"easydeploy/internal/xds"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
)

// Deps bundles the server's dependencies.
type Deps struct {
	Cfg        config.Config
	Docker     *docker.Client // the local host client
	Registry   *proxy.Registry
	Tunnels    *tunnel.Manager
	Store      *store.Store
	Auth       *auth.Manager
	Registries *registry.Service
	Services   *service.Manager
	Endpoints  *endpoint.Manager
	XDS        *xds.Manager
}

// Server holds the dependencies shared by all HTTP handlers.
type Server struct {
	cfg        config.Config
	docker     *docker.Client // local host (services, quota, proxy sync)
	registry   *proxy.Registry
	tunnels    *tunnel.Manager
	store      *store.Store
	auth       *auth.Manager
	registries *registry.Service
	services   *service.Manager
	endpoints  *endpoint.Manager
	xds        *xds.Manager
	upgrader   websocket.Upgrader
}

// NewServer constructs the API server.
func NewServer(d Deps) *Server {
	return &Server{
		cfg:        d.Cfg,
		docker:     d.Docker,
		registry:   d.Registry,
		tunnels:    d.Tunnels,
		store:      d.Store,
		auth:       d.Auth,
		registries: d.Registries,
		services:   d.Services,
		endpoints:  d.Endpoints,
		xds:        d.XDS,
		upgrader: websocket.Upgrader{
			// Dev default: accept any origin. Tighten for production.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// endpointID reads the target environment id from the request (header or
// query). Empty / "local" means the local host.
func endpointID(r *http.Request) int64 {
	v := r.Header.Get("X-Endpoint-Id")
	if v == "" {
		v = r.URL.Query().Get("endpoint")
	}
	if v == "" || v == "local" {
		return endpoint.LocalID
	}
	id, _ := strconv.ParseInt(v, 10, 64)
	return id
}

// clientFor resolves the docker client for the request's target environment.
func (s *Server) clientFor(r *http.Request) (*docker.Client, error) {
	return s.endpoints.ClientFor(r.Context(), endpointID(r))
}

// requireEndpointAccess blocks a member from targeting an environment they
// weren't granted. The local host (0) is always allowed; admins bypass. The
// target comes from the X-Endpoint-Id header / ?endpoint= query (see endpointID).
func (s *Server) requireEndpointAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := endpointID(r)
		if id == endpoint.LocalID {
			next.ServeHTTP(w, r)
			return
		}
		p := auth.Current(r.Context())
		if p.IsAdmin() {
			next.ServeHTTP(w, r)
			return
		}
		ok, err := s.store.UserHasEndpoint(r.Context(), p.UserID, id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			writeErr(w, http.StatusForbidden, errNoEndpointAccess)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// dockerOr502 resolves the target environment's client, writing a 502 and
// returning ok=false on failure.
func (s *Server) dockerOr502(w http.ResponseWriter, r *http.Request) (*docker.Client, bool) {
	cli, err := s.clientFor(r)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return nil, false
	}
	return cli, true
}

// Routes builds the HTTP handler tree.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Route("/api", func(r chi.Router) {
		// Public endpoints.
		r.Get("/health", s.handleHealth)
		r.Post("/auth/login", s.handleLogin)
		// Git webhook: authenticated by an unguessable per-service token in
		// the path, so it lives outside the JWT middleware.
		r.Post("/hooks/{token}", s.handleWebhook)

		// Everything else requires a valid session token.
		r.Group(func(r chi.Router) {
			r.Use(s.auth.Middleware)
			// A member may only target the local host or an environment
			// explicitly granted to them; admins may target any.
			r.Use(s.requireEndpointAccess)

			// Current user + resource requests (any authenticated user).
			r.Get("/me", s.handleMe)
			r.Post("/requests", s.handleCreateRequest)
			r.Get("/requests", s.handleListRequests)
			// Environments a user may see/switch to (filtered by grants for
			// members). Management stays admin-only (below).
			r.Get("/endpoints", s.handleListEndpoints)
			r.Get("/endpoints/{id}/status", s.handleEndpointStatus)

			// Read-only views available to members and admins. Members see
			// only their own containers (filtered in the handler). Networks,
			// volumes, and images are shared infrastructure — admin-only (below).
			r.Get("/containers", s.handleListContainers)

			// Services (load-balanced, autoscaled, git-deployable). Members
			// manage their own; admins manage all. Ownership is checked in
			// the handlers.
			r.Get("/services", s.handleListServices)
			r.Post("/services", s.handleCreateService)
			r.Get("/services/{name}", s.handleGetService)
			r.Put("/services/{name}", s.handleUpdateService)
			r.Post("/services/{name}/scale", s.handleScaleService)
			r.Post("/services/{name}/redeploy", s.handleRedeployService)
			r.Post("/services/{name}/subdomain", s.handleSetServiceSubdomain)
			r.Delete("/services/{name}", s.handleDeleteService)

			// Container-scoped actions. For members the ownership middleware
			// restricts these to containers they own; admins bypass it.
			r.Group(func(r chi.Router) {
				r.Use(s.requireContainerOwner)
				r.Get("/containers/{id}", s.handleInspectContainer)
				r.Post("/containers/{id}/start", s.handleContainerAction((*docker.Client).Start))
				r.Post("/containers/{id}/stop", s.handleContainerAction((*docker.Client).Stop))
				r.Post("/containers/{id}/restart", s.handleContainerAction((*docker.Client).Restart))
				r.Put("/containers/{id}", s.handleEditContainer)
				r.Post("/containers/{id}/update", s.handleUpdateContainer)
				r.Delete("/containers/{id}", s.handleContainerAction((*docker.Client).Remove))
				r.Get("/containers/{id}/logs", s.handleLogs)   // WebSocket
				r.Get("/containers/{id}/stats", s.handleStats) // WebSocket
				r.Get("/containers/{id}/exec", s.handleExec)   // WebSocket (shell)
			})

			// Volumes, networks, and registries are usable by members but
			// scoped to what they own: lists are filtered to the caller's
			// resources, creates stamp the caller as owner, and the by-id/name
			// routes are guarded by ownership middleware. Admins see/manage all.
			r.Group(func(r chi.Router) {
				// Volumes.
				r.Get("/volumes", s.handleListVolumes)
				r.Get("/volumes/usage", s.handleVolumeUsage)
				r.Get("/volume-names", s.handleVolumeNames)
				r.Post("/volumes", s.handleCreateVolume)
				r.Group(func(r chi.Router) {
					r.Use(s.requireVolumeOwner)
					r.Get("/volumes/{name}", s.handleInspectVolume)
					r.Post("/volumes/{name}/resize", s.handleResizeVolume)
					r.Get("/volumes/{name}/browse", s.handleBrowseVolume)
					r.Post("/volumes/{name}/mkdir", s.handleMkdirVolume)
					r.Post("/volumes/{name}/upload", s.handleUploadVolume)
					r.Get("/volumes/{name}/download", s.handleDownloadVolume)
					r.Delete("/volumes/{name}/file", s.handleDeleteVolumeFile)
					r.Delete("/volumes/{name}", s.handleRemoveVolume)
				})

				// Networks.
				r.Get("/networks", s.handleListNetworks)
				r.Post("/networks", s.handleCreateNetwork)
				r.Group(func(r chi.Router) {
					r.Use(s.requireNetworkOwner)
					r.Get("/networks/{id}", s.handleInspectNetwork)
					r.Delete("/networks/{id}", s.handleRemoveNetwork)
					r.Post("/networks/{id}/connect", s.handleConnectNetwork)
					r.Post("/networks/{id}/disconnect", s.handleDisconnectNetwork)
				})

				// Registries.
				r.Get("/registries", s.handleListRegistries)
				r.Post("/registries", s.handleCreateRegistry)
				r.Post("/registries/test", s.handleTestRegistry)
				r.Group(func(r chi.Router) {
					r.Use(s.requireRegistryOwner)
					r.Delete("/registries/{id}", s.handleDeleteRegistry)
					r.Get("/registries/{id}/catalog", s.handleRegistryCatalog)
					r.Get("/registries/{id}/tags", s.handleRegistryTags)
				})
			})

			// Admin-only: user management, request review, and shared
			// infrastructure (images, routes, tunnels).
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAdmin)

				// Images remain admin-only shared infrastructure.
				r.Get("/images", s.handleListImages)

				r.Get("/users", s.handleListUsers)
				r.Post("/users", s.handleCreateUser)
				r.Put("/users/{id}/quota", s.handleUpdateUserQuota)
				r.Put("/users/{id}/role", s.handleSetUserRole)
				r.Put("/users/{id}/password", s.handleResetPassword)
				r.Get("/users/{id}/environments", s.handleGetUserEnvironments)
				r.Put("/users/{id}/environments", s.handleSetUserEnvironments)
				r.Delete("/users/{id}", s.handleDeleteUser)

				// Environments (multi-host). List + status are
				// in the shared group above; management is admin-only.
				r.Post("/endpoints", s.handleCreateEndpoint)
				r.Put("/endpoints/{id}", s.handleUpdateEndpoint)
				r.Delete("/endpoints/{id}", s.handleDeleteEndpoint)
				// Edge proxy: an Envoy on a remote host, driven by this xDS, so
				// Routes/Services work on that environment.
				r.Get("/endpoints/{id}/edge", s.handleEdgeStatus)
				r.Post("/endpoints/{id}/edge", s.handleDeployEdge)
				r.Delete("/endpoints/{id}/edge", s.handleRemoveEdge)

				r.Post("/requests/{id}/review", s.handleReviewRequest)

				r.Delete("/images/{id}", s.handleRemoveImage)

				r.Get("/routes", s.handleListRoutes)
				r.Post("/routes", s.handleUpsertRoute)
				r.Delete("/routes/{subdomain}", s.handleDeleteRoute)

				r.Get("/public-ip", s.handlePublicIP)
				r.Get("/tunnels", s.handleListTunnels)
				r.Post("/tunnels", s.handleCreateTunnel)
				r.Post("/tunnels/{id}/start", s.handleStartTunnel)
				r.Post("/tunnels/{id}/stop", s.handleStopTunnel)
				r.Delete("/tunnels/{id}", s.handleDeleteTunnel)
			})
		})
	})

	// Serve the built single-page app when a web directory is configured.
	// Unknown non-API paths fall back to index.html so client-side routing
	// works.
	if s.cfg.WebDir != "" {
		s.mountSPA(r)
	}
	return r
}

// mountSPA serves static assets from cfg.WebDir with an index.html fallback.
func (s *Server) mountSPA(r chi.Router) {
	fs := http.FileServer(http.Dir(s.cfg.WebDir))
	index := filepath.Join(s.cfg.WebDir, "index.html")
	r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := filepath.Join(s.cfg.WebDir, filepath.Clean(req.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			// Vite fingerprints /assets/* by content hash, so they can be cached
			// forever; everything else (index.html especially) must revalidate
			// so a new build is picked up without a hard refresh.
			if strings.HasPrefix(req.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fs.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, req, index)
	}))
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
