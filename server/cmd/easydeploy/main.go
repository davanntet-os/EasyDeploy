// Command easydeploy is the EasyDeploy server: a Docker management platform
// with a built-in Envoy control plane for subdomain routing and public
// exposure via WiFi or a cloud SSH tunnel.
//
// It runs two listeners:
//   - an HTTP server for the REST/WebSocket API and web UI, and
//   - a gRPC server that serves Envoy's dynamic xDS configuration.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"easydeploy/internal/api"
	"easydeploy/internal/auth"
	"easydeploy/internal/config"
	"easydeploy/internal/docker"
	"easydeploy/internal/endpoint"
	"easydeploy/internal/proxy"
	"easydeploy/internal/registry"
	"easydeploy/internal/secret"
	"easydeploy/internal/service"
	"easydeploy/internal/store"
	"easydeploy/internal/tunnel"
	"easydeploy/internal/xds"

	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("easydeploy: %v", err)
	}
}

func run() error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	dockerCli, err := docker.New(cfg.DockerHost)
	if err != nil {
		return err
	}
	defer dockerCli.Close()

	box, err := secret.New(cfg.SecretKey)
	if err != nil {
		return err
	}
	// Bootstrap the initial admin account on first run.
	if err := bootstrapAdmin(ctx, st, cfg.AdminPassword); err != nil {
		return err
	}
	authMgr, err := auth.New(st, cfg.JWTSecret)
	if err != nil {
		return err
	}
	registrySvc := registry.New(st, box)
	endpoints := endpoint.New(st, box, dockerCli)

	xdsMgr := xds.NewManager(cfg.EnvoyNodeID, cfg.BaseDomain)
	proxyReg := proxy.New(st, xdsMgr, cfg.BaseDomain)
	tunnels := tunnel.NewManager("")

	// The service manager owns replica-based apps (load balancing, autoscale,
	// git deploys). It resolves the right Docker host per service via the
	// endpoint manager, and contributes multi-endpoint upstreams to the proxy.
	serviceMgr := service.New(st, endpoints, proxyReg, registrySvc)
	proxyReg.SetServiceSource(serviceMgr)

	// Reconcile services (recreate missing replicas) and publish all routes so
	// Envoy has config as soon as it connects.
	if err := serviceMgr.SyncAll(ctx); err != nil {
		log.Printf("warning: initial service sync failed: %v", err)
	}
	if err := proxyReg.Sync(ctx); err != nil {
		log.Printf("warning: initial route sync failed: %v", err)
	}

	// Start the CPU autoscaling control loop.
	go serviceMgr.RunAutoscaler(ctx, 20*time.Second)

	// Start the xDS gRPC control plane.
	grpcSrv := grpc.NewServer()
	xdsMgr.Register(grpcSrv)
	grpcLis, err := net.Listen("tcp", cfg.XDSAddr)
	if err != nil {
		return err
	}
	go func() {
		log.Printf("xds control plane listening on %s (node=%s)", cfg.XDSAddr, cfg.EnvoyNodeID)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			log.Printf("xds server stopped: %v", err)
		}
	}()

	// Start the HTTP API server.
	apiSrv := api.NewServer(api.Deps{
		Cfg:        cfg,
		Docker:     dockerCli,
		Registry:   proxyReg,
		Tunnels:    tunnels,
		Store:      st,
		Auth:       authMgr,
		Registries: registrySvc,
		Services:   serviceMgr,
		Endpoints:  endpoints,
		XDS:        xdsMgr,
	})
	httpSrv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: apiSrv.Routes(),
	}
	go func() {
		log.Printf("http api listening on %s (base domain=%s)", cfg.HTTPAddr, cfg.BaseDomain)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server stopped: %v", err)
		}
	}()

	// Block until interrupted, then shut down gracefully.
	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	grpcSrv.GracefulStop()
	dockerCli.CleanupVolumeHelpers(shutdownCtx)
	return nil
}

// bootstrapAdmin creates the initial admin account (username "admin") from the
// configured password if no users exist yet.
func bootstrapAdmin(ctx context.Context, st *store.Store, adminPassword string) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := auth.HashPassword(adminPassword)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, store.User{
		Username:     "admin",
		PasswordHash: hash,
		Role:         store.RoleAdmin,
	}); err != nil {
		return err
	}
	log.Println("bootstrapped initial admin account (username: admin)")
	return nil
}
