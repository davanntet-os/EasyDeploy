<p align="center">
  <img src="assets/logo.svg" width="96" alt="EasyDeploy logo">
</p>

<h1 align="center">EasyDeploy</h1>

<p align="center"><em>Deploy a container, get a subdomain.</em></p>

A self-hosted Docker management platform
with a built-in **Envoy** reverse proxy that gives every deployed container its
own **subdomain**, and one-click **public exposure** via either your WiFi/NAT
public IP or a **cloud VM SSH reverse tunnel**.

## What makes it different

- **Multi-host management** — manage several Docker daemons from one
  place. Add remote environments (Docker API over TCP, optional mutual TLS) and
  switch between them; container/image/volume/network management targets the
  selected host.


- **Manage the Docker daemon** — list/start/stop/restart/**update**/remove
  containers, stream logs, browse images, and create/delete **networks**,
  all from the UI.
- **Volume management** — create, delete, see each volume's size and how many
  containers use it, and a **file manager**: browse, create folders, upload,
  download, and delete files inside a volume.
- **Interactive shell** — open a real terminal (xterm.js) into any running
  container, straight from the browser.
- **Live monitoring** — per-container CPU, memory (vs. its limit), and network
  meters with sparklines, streamed live.
- **Automatic subdomains** — deploy with a subdomain and container port, and
  EasyDeploy programs Envoy (live, over xDS gRPC — no restart) to route
  `myapp.<base-domain>` to that container.
- **Services: load balancing & autoscaling** — a Service runs N identical
  replica containers behind one subdomain; Envoy round-robins across them.
  Optionally autoscale replicas on average CPU between min/max bounds.
- **Git webhooks** — give a Service a repo URL and it generates a push webhook
  (`/api/hooks/<token>`) that clones the branch, builds the image from its
  Dockerfile, and rolls the new version out across replicas.
- **Container updates** — pull a (possibly new) image tag and recreate a
  container in place, preserving its config, ports, and network attachments.
- **Private registries** — store registry credentials (AES-GCM encrypted),
  use them for authenticated pulls, test logins, and browse repos/tags via the
  registry v2 API.
- **Users & RBAC** — multi-user with `admin` and `member` roles, bcrypt
  passwords, JWT sessions. Members only see and control their own containers.
- **Resource requests & quotas** — members must request a CPU/RAM quota; an
  admin approves it, and every member deploy is then hard-capped (Docker
  `NanoCPUs` + `Memory`) and blocked once the quota is exhausted.
- **Public exposure, two ways**
  1. **WiFi / NAT public IP** — detect your public IP and port-forward :80.
  2. **Cloud VM SSH tunnel** — open a reverse tunnel to any cloud VM so its
     public IP forwards inbound traffic to your local Envoy, no router config.

## Architecture

```
Browser ──HTTP/WS──► Go server (:8080)  ─────────────► Docker daemon
                        │  REST + WebSocket API           (containers/images)
                        │
                        ├── proxy registry ──► xDS control plane (gRPC :18000)
                        │                              │  live snapshots
                        │                              ▼
Public traffic ──:80──► Envoy (:10000) ◄──────── dynamic listeners/routes/clusters
                        │
                        └── tunnel manager ──► SSH reverse tunnel ──► Cloud VM
```

- `server/` — Go backend: Docker SDK wrapper, REST/WebSocket API, Envoy xDS
  control plane, proxy route registry, SSH tunnel manager, auth, registry
  service, PostgreSQL persistence.
- `web/` — React + Vite + TypeScript UI.
- `deploy/` — Envoy bootstrap config and docker-compose stack (incl. Postgres).

## Quick start (development)

```bash
# 0. Postgres (once)
make postgres

# 1. Backend (needs a running Docker daemon)
EASYDEPLOY_ADMIN_PASSWORD=admin123 EASYDEPLOY_SECRET_KEY=dev-key make dev-server   # :8080

# 2. Frontend
make dev-web            # http://localhost:5173 (proxies /api to the server)

# 3. Envoy data plane (in Docker, points at the host control plane)
make envoy              # serves subdomains on http://localhost
```

Sign in as **`admin`** with your `EASYDEPLOY_ADMIN_PASSWORD`, then **Services →
New service**: image `nginx:alpine`, subdomain `blog`, port `80`. Visit
`http://blog.localhost` (Chrome resolves `*.localhost` automatically; for other
browsers add `blog.localhost` to `/etc/hosts`). Create member accounts and
approve their resource requests from the **Users** and **Requests** tabs.

## Full stack

```bash
export EASYDEPLOY_ADMIN_PASSWORD=change-me
export EASYDEPLOY_SECRET_KEY=$(openssl rand -hex 32)
EASYDEPLOY_BASE_DOMAIN=apps.example.com make up
```

## Configuration

| Env var | Default | Purpose |
| --- | --- | --- |
| `EASYDEPLOY_HTTP_ADDR` | `:8080` | API + UI listen address |
| `EASYDEPLOY_XDS_ADDR` | `:18000` | Envoy xDS gRPC address |
| `EASYDEPLOY_DATABASE_URL` | `postgres://easydeploy:easydeploy@localhost:5432/easydeploy?sslmode=disable` | PostgreSQL connection string |
| `EASYDEPLOY_ADMIN_PASSWORD` | _(required)_ | Bootstrap password for the initial `admin` user (first run only) |
| `EASYDEPLOY_SECRET_KEY` | _(required)_ | Master key for AES-GCM secret encryption |
| `EASYDEPLOY_JWT_SECRET` | _(random)_ | JWT signing key (random per-process if unset) |
| `EASYDEPLOY_BASE_DOMAIN` | `localhost` | Wildcard domain for subdomains |
| `EASYDEPLOY_ENVOY_NODE_ID` | `easydeploy-envoy` | Must match Envoy `--service-node` |
| `EASYDEPLOY_XDS_ADVERTISE_ADDR` | _(unset)_ | This machine's LAN-reachable `host:port` for the xDS port, e.g. `10.0.0.5:18000`. Required to deploy a **remote edge proxy** (Routes/Services on a remote host) — a remote Envoy dials this address for its config |
| `EASYDEPLOY_WEB_DIR` | _(unset)_ | Serve built UI from this dir |
| `EASYDEPLOY_SSH_KEY` | _(unset)_ | Private key for SSH tunnels |
| `DOCKER_HOST` | _(SDK default)_ | Docker daemon endpoint |

The server refuses to start without `EASYDEPLOY_ADMIN_PASSWORD` and
`EASYDEPLOY_SECRET_KEY` set.

## Routes & Services on remote hosts

Subdomain routing and load-balanced services work on remote environments too,
via a small **edge Envoy** deployed on each remote host and driven by the same
central control plane:

1. Set `EASYDEPLOY_XDS_ADVERTISE_ADDR` to this machine's LAN-reachable
   `host:port` for the xDS port (e.g. `10.0.0.5:18000`). The remote host must be
   able to reach it.
2. Add the remote environment and select it in the switcher.
3. On the **Routes** or **Services** tab, use the **edge-proxy banner** to
   *Deploy edge proxy*. This runs `envoyproxy/envoy` on the remote host, joined
   to its `easydeploy-edge` network, dialing back to your control plane.
4. Create routes/services as usual — replicas run on the remote host and its
   edge Envoy serves the subdomains.

Every service gets two kinds of address:

- an **automatic host** `‹service›.‹server›.‹base-domain›` (always present, e.g.
  `web.llm-server.example.com`), namespaced by the host it runs on; and
- an **optional custom subdomain** `‹subdomain›.‹base-domain›` you can set at
  create time or change later — it's additive, the auto host stays.

Point DNS for a host at that environment's edge proxy, or test without DNS using
`curl -H "Host: web.llm-server.example.com" http://‹host-ip›:‹edge-port›/`.

Public **Expose** (WiFi IP / SSH tunnel) remains local-only for now. Remote
hosts that can't reach the control plane (strict NAT/firewall) need a future
deployed agent.

## Securing the Docker daemon

**Access to a Docker daemon is root on that host.** When adding remote
environments, choose the connection mode accordingly (the "Add environment"
form offers all three):

- **SSH (`ssh://user@host`) — recommended.** Tunnels the Docker API over SSH
  using the EasyDeploy host's SSH keys/agent. No Docker port is exposed. Ensure
  the server can `ssh` to the target and that user can use Docker.
- **TCP + mutual TLS (`tcp://host:2376`).** Docker's authenticated transport.
  EasyDeploy **requires a CA** so the daemon's identity is verified — it will
  not connect insecurely. Generate certs per Docker's
  [protect-access guide](https://docs.docker.com/engine/security/protect-access/)
  and firewall the port to the EasyDeploy server's IP.
- **Plaintext TCP (`tcp://host:2375`).** Unauthenticated root access — only on
  a trusted, isolated network, never over the internet.

Also protect EasyDeploy itself (it's the gateway to every host): serve its API
over HTTPS, keep the secrets above strong, and don't expose port 8080 publicly.
Consider running the daemon **rootless** or with **userns-remap**, and putting a
**docker-socket-proxy** in front of the local socket to limit blast radius.

## License

EasyDeploy is licensed under the **[Apache License 2.0](LICENSE)**.
Copyright © 2026 Davann Tet.

You are free to use, modify, and redistribute it (including commercially), but
you must:

- **keep the copyright and [`NOTICE`](NOTICE)** — you may not remove the original
  author's copyright or claim authorship of the original work;
- **state your changes** — mark any files you modify as changed;
- **not use the "EasyDeploy" name or marks** to endorse or promote your fork
  (the license grants no trademark rights).

See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE) for the full terms.
