# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

EasyDeploy is a Docker management platform with a built-in Envoy reverse proxy.
Beyond Portainer-style container management, its distinguishing feature is that
deploying a container can automatically publish it on a **subdomain** (via a
live Envoy **xDS control plane**) and expose it publicly through either the
local **WiFi/NAT public IP** or a **cloud VM SSH reverse tunnel**.

## Commands

Backend (`server/`, Go 1.26):

```bash
make postgres       # start a local Postgres (once)
make dev-server     # go run — needs Docker daemon + Postgres; sets dev admin pw + secret key
make build          # build the binary
make vet            # go vet ./...
make tidy           # go mod tidy
cd server && go test ./...                    # all tests
cd server && go test ./internal/xds -run TestX # a single test
```

The server refuses to start unless `EASYDEPLOY_ADMIN_PASSWORD` and
`EASYDEPLOY_SECRET_KEY` are set (see `config.Validate`). `make dev-server`
supplies dev defaults.

Frontend (`web/`, React + Vite + TypeScript):

```bash
make dev-web        # vite dev server on :5173, proxies /api → :8080
cd web && npm run build   # tsc -b && vite build (strict TS; build fails on type errors)
```

Full stack / Envoy:

```bash
make envoy          # run Envoy in Docker against the host control plane
make up / make down # docker compose stack (deploy/docker-compose.yml)
```

## Architecture — the parts that span files

The single most important flow to understand is **how a deploy becomes a live
public subdomain**, because it threads through five packages:

1. `POST /api/services` → `handleCreateService` → `service.Manager.Create`
   reconciles N replica containers (`docker.Client.Deploy`, labeled
   `easydeploy.service` / `easydeploy.subdomain` / `easydeploy.managed`).
   (Containers can also be recreated via `PUT /api/containers/{id}` →
   `handleEditContainer`. There is **no** standalone single-container deploy
   endpoint any more — it was retired in favor of services with `replicas: 1`.)
2. Routing upstreams come from two sources merged in `proxy.Registry.publish`:
   the `service.Manager` (via `SetServiceSource`, one subdomain → many replica
   hosts = the load balancer) and any persisted manual `store.Route`s.
3. `proxy.Registry` calls its `Snapshotter` (the `xds.Manager`) with the
   **full** set of upstreams. It always republishes everything, never a delta.
4. `xds.Manager.Update` translates upstreams into Envoy resources
   (Listener + RouteConfiguration + Clusters), bumps a version counter, and
   pushes a new snapshot into the go-control-plane cache.
5. Envoy — configured by `deploy/envoy.yaml` to fetch **all** dynamic config
   over gRPC from `:18000` — receives the snapshot and reconfigures live.

Key cross-cutting facts:

- **`proxy.Registry` is the source of truth for routing**, not Envoy and not the
  DB directly. It holds a mutex and is the only thing that calls `xds.Update`.
  On startup, `main.go` calls `registry.Sync` so persisted routes are pushed to
  Envoy before Envoy even connects.
- **The `xds.Upstream.Host` is a container *name*, used as a STRICT_DNS target.**
  This only resolves if Envoy and the managed container share a Docker network
  (`easydeploy-edge`, created by the compose file). This is why `Deploy` and the
  UI default the network to `easydeploy-edge`.
- **The Envoy node id must match on both sides**: `EASYDEPLOY_ENVOY_NODE_ID`
  (server) and `--service-node` / `node.id` in `deploy/envoy.yaml`. A mismatch
  means Envoy gets an empty config and silently serves 404s.
- **Two independent listeners in `main.go`**: the HTTP API (`:8080`) and the xDS
  gRPC server (`:18000`). They share the `xds.Manager` and `proxy.Registry`.
- **Public exposure is separate from routing.** `tunnel.Manager` (SSH reverse
  tunnels + public-IP detection) forwards raw TCP to Envoy's local port; it
  knows nothing about subdomains. Envoy still demuxes by Host header.

## Multi-environment (Portainer-style multi-host)

EasyDeploy manages **multiple Docker hosts** from one instance (`internal/endpoint`).

- An **environment** is a Docker daemon. Id `0` is always the **local** host
  (`endpoint.LocalID`, the client wired at startup). Remote environments are
  rows in the `endpoints` table (`store/endpoints.go`) — a Docker-API `host`
  (e.g. `tcp://10.0.0.5:2376`) plus optional TLS client material, **encrypted**
  with the same `secret.Box` as registry passwords.
- `endpoint.Manager` caches a `docker.Client` per environment and builds remote
  clients via `docker.NewRemote` (`docker/remote.go`), which supports three
  connection modes:
  - **`ssh://user@host[:port]`** — tunnels the Docker API over SSH via
    `docker/cli/connhelper` (runs the local `ssh` binary → `docker system
    dial-stdio`). No open Docker port; the safest option. **Recommended.** A
    non-22 port is honored (connhelper adds `-p`); the Add-environment form has a
    dedicated optional **SSH port** field that composes it into the host string.
    Uses `GetConnectionHelperWithSSHOpts` with **SSH connection multiplexing**
    (`ControlMaster=auto`, `ControlPath=/tmp/ed-ssh-%C`, `ControlPersist=5m`)
    so every Docker call reuses one SSH connection instead of paying a fresh
    handshake+auth each time — without this, remote pages are painfully slow
    (each API call = a new `ssh` process). Also `BatchMode=yes` +
    `StrictHostKeyChecking=accept-new` so it never hangs on a prompt.
  - **`tcp://host:2376` + TLS** — mutual TLS. A **CA is required** (we never
    fall back to `InsecureSkipVerify` — that would allow MITM). Cert/key/CA are
    PEM, encrypted at rest.
  - **`tcp://host:2375`** — plaintext, unauthenticated; trusted networks only.
  `endpoint.Manager.Status` is bounded (8s) so a dead SSH/TCP host can't hang.
- **Every request selects its environment**: the frontend sends
  `X-Endpoint-Id` (WebSockets use `?endpoint=`, see `api.ts` `environment` +
  `wsURL`); the server resolves it in `api.server.go` `endpointID` /
  `clientFor` / `dockerOr502`. Container/image/volume/network handlers all go
  through the resolved client — **not** `s.docker` (which stays the local host
  for services, quota `OwnerUsage`, and proxy sync).
- **What is per-environment vs local-only**: container/image/volume/network
  management **and now Services + Routes** are per-environment. Only **public
  Expose** stays local-only (it depends on the local host's own network/tunnel);
  the UI hides just that tab (`LOCAL_ONLY_TABS = ["expose"]`) on a remote env.
- **Routes/Services on remote hosts = a per-host edge Envoy driven by the same
  xDS** (Phase 1 of multi-host routing). See the "Remote edge proxy" section
  below.
- Remote connection is the **direct Docker API** (SSH / TCP+TLS / plaintext).
  A deployed agent (for NAT/edge hosts that can't reach the control plane, so
  the edge Envoy's gRPC dial-in wouldn't work) would be the next step for
  Phase 3; not built yet.
- The UI switcher (`EnvSwitcher`) keys `<main>` by env id so switching hosts
  remounts the views and refetches. Members don't switch (admins only).

## Remote edge proxy (Routes/Services on remote hosts)

Making a subdomain route to containers on a **remote** host needs an Envoy
*next to those containers* (STRICT_DNS resolves replica names on that host's
`easydeploy-edge` network). Phase 1 ("per-host Envoy via central xDS") deploys
one edge Envoy per remote environment, all driven by the **single central xDS**:

- **One Envoy node per environment.** `xds.Manager.NodeForEndpoint(id)` maps
  env 0 → the configured local node id, env N → `easydeploy-edge-<N>`. The
  go-control-plane `SnapshotCache` (keyed by `IDHash` on node id) already serves
  a distinct snapshot per node; `xds.Manager.Update(ctx, nodeID, ups)` sets one.
- **`xds.Upstream.EndpointID`** tags each upstream with its environment (grouping
  only — ignored when building Envoy resources). `store.Route` and
  `store.Service` carry `endpoint_id` (additive migration, default 0 = local).
- **`proxy.Registry.publish` groups upstreams by env and pushes one snapshot per
  node** — the local node plus every row in `endpoints` (so a deleted last route
  clears its node). A remote node with no connected Envoy just keeps its snapshot
  cached until one dials in.
- **`service.Manager` is environment-aware**: it resolves
  `endpoints.ClientFor(svc.EndpointID)` in reconcile/redeploy/delete/scaler/git-
  build, so replicas run on the right host. Members can't switch env (admin-only),
  so quota (local `OwnerUsage`) never spans hosts.
- **Deploying the edge Envoy** (`docker.Client.DeployEdge`, admin API
  `POST /endpoints/{id}/edge`): ensures the `easydeploy-edge` network on the
  remote, (re)creates an `envoyproxy/envoy` container named
  `easydeploy-edge-proxy`, injects a generated bootstrap via `CopyToContainer`
  (create → copy `/bootstrap.yaml` → start), publishes 10000 → a host port
  (default 80). `GET`/`DELETE` report/remove it. `xds.EdgeBootstrapYAML` embeds
  the node id and points the xDS cluster at **`EASYDEPLOY_XDS_ADVERTISE_ADDR`**.
- **`EASYDEPLOY_XDS_ADVERTISE_ADDR` is required for remote edges** — the host:port
  a *remote* Envoy dials to reach this control plane's `:18000`. It must be this
  machine's LAN-reachable address; from the remote host `host.docker.internal`
  points at the wrong machine. Phase 1 assumes the remote can reach the center
  (LAN). A remote behind NAT can't dial in → that's the Phase 3 agent.
- **Frontend**: `EdgeBanner` (atop Routes/Services on a remote env) shows edge
  status and deploy/redeploy/remove; service/route create send `X-Endpoint-Id`
  and the handlers stamp/scope by `endpointID(r)`.

### Service host names (auto + custom)

Every service is reachable at **two** kinds of host, built in
`proxy.Registry.publish` and surfaced by `Registry.ServiceDomains`:

- **Auto host — always present**: `<service>.<server-slug>.<base-domain>`. The
  server slug is `slug(endpoint.Name)`; the local host (env 0) is `local`. This
  namespaces subdomains by host, so the same service name on two hosts never
  collides. A service needs **no** custom subdomain to be routable.
- **Custom subdomain — optional, adds a 2nd host**: `<subdomain>.<base-domain>`.
  Settable at create (`subdomain` field, optional) or later via
  `POST /services/{name}/subdomain` (`service.Manager.SetSubdomain`, validated as
  a single DNS label, empty clears it). It is **additive** — the auto host stays.

`xds.Upstream` carries `Key` (names the Envoy cluster/vhost — `svc-<slug>-<name>`
for services, `rt-<subdomain>` for manual routes) and `Domains []string` (the
full FQDNs to match). `makeRouteConfig` dedupes domains per node (Envoy rejects a
route config with two vhosts claiming the same Host). The `/services` responses
are wrapped in `serviceView` to include the computed `domains`. Manual routes
are unchanged (`<subdomain>.<base-domain>`, no auto host).

## Docker SDK version pin (important, easy to break)

`go.mod` pins **`github.com/docker/docker v27.3.1+incompatible`** and
**`github.com/docker/go-connections v0.5.0`** deliberately. Newer Docker modules
split `api/` and `client/` into separate modules whose paths now resolve to
`github.com/moby/moby`, and v28 renamed the types this code uses
(`types.Container` → `container.Summary`, `types.ContainerJSON` →
`container.InspectResponse`). If you run `go get -u` or let `go mod tidy` bump
Docker, the build breaks with "module declares its path as
github.com/moby/moby/api" or "undefined: sockets.DialPipe". Keep the pins, or
migrate the `docker` package's type names deliberately.

## Auth, secrets, and registries (added after the SQLite→Postgres migration)

- **Persistence is PostgreSQL via `jackc/pgx/v5` (`pgxpool`)**, not SQLite.
  Queries use `$1` placeholders and `RETURNING` for generated ids;
  `store.Open` takes a context and connection URL. Migrations run on startup
  as `CREATE TABLE IF NOT EXISTS` in `store.migrate`.
- **Auth is multi-user with RBAC + JWT** (`internal/auth`). Users live in
  Postgres with bcrypt password hashes and a role (`admin`/`member`). The JWT
  carries `uid` + `role`. On first run `main.bootstrapAdmin` creates the
  `admin` account from `EASYDEPLOY_ADMIN_PASSWORD`. The whole `/api` tree is
  gated by `auth.Manager.Middleware` except `/health` and `/auth/login`;
  `auth.RequireAdmin` further gates the admin-only subtree (users, request
  review, all infra: networks/routes/registries/tunnels). WebSocket clients
  can't set an Authorization header, so the middleware also accepts `?token=`.
- **Members are quota-bound; admins are not.** The three-tier route groups in
  `api/server.go` are: (1) any authenticated user — `/me`, `/requests`,
  read-only lists, `/services` (create/scale/redeploy/delete their own);
  (2) container-scoped actions wrapped by
  `requireContainerOwner` (members may only touch containers labeled
  `easydeploy.owner=<them>`; admins bypass); (3) `RequireAdmin` for everything
  else. `handleListContainers` also filters to owned containers for members.
- **The resource-request / quota workflow** is the headline member flow:
  a member with no quota cannot deploy (`resolveResources` returns 403). They
  `POST /requests` (CPU millicores + MB); an admin approves via
  `/requests/{id}/review`, which calls `UpdateUserQuota` — approval *grants a
  quota*, it does not deploy. The member then deploys within it. Every member
  deploy/edit passes through `resolveResources`, which sums their live usage
  via `docker.OwnerUsage` (label-filtered inspect, the source of truth — not
  the DB) and rejects anything that would exceed the quota. Limits are
  hard-enforced as Docker `NanoCPUs` + `Memory`. Conversions:
  1000 millicores = 1 core = 1e9 NanoCPUs; MB = bytes >> 20.
- **Registry passwords are encrypted at rest** with AES-256-GCM
  (`internal/secret`, key = SHA-256 of `EASYDEPLOY_SECRET_KEY`). The
  `store.Registry.PasswordEnc` column holds ciphertext and is `json:"-"` so it
  never reaches clients. `registry.Service` owns the plaintext↔ciphertext
  boundary; the store never sees plaintext.
- **Deploys and updates auto-attach registry auth**: `handleDeploy` /
  `handleUpdateContainer` call `registry.Service.AuthForImage`, which matches
  the image host against configured registries (Docker Hub aliases handled in
  `hostsMatch`) and returns a base64 Docker auth token.
- **Container "update" = recreate in place** (`docker.Recreate`): inspect →
  pull image → stop+remove old → recreate under the same name with the same
  config/hostconfig and re-attached networks → start. Docker containers are
  immutable, so this is the standard replace pattern. The subdomain route
  survives because the new container keeps the name the STRICT_DNS cluster
  resolves.

## Services: load balancing, autoscaling, git webhooks (`internal/service`)

A **Service** (`store.services` table, `service.Manager`) is a replica-managed
app layered on top of the single-container primitives. It is the unit for the
three advanced features and threads through the proxy just like a manual route:

- **Load balancing** = one Envoy cluster with **many endpoints**. `xds.Upstream`
  carries `Hosts []string`; `service.Manager.Upstreams` (registered on the
  registry via `proxy.Registry.SetServiceSource`) maps each service's subdomain
  to its replica container names `<svc>-<0..N-1>`. `registry.publish` merges
  manual routes (single host) with service upstreams (multi-host); services win
  on subdomain conflict. Envoy round-robins (`Cluster_ROUND_ROBIN`).
- **`Upstreams` uses the *desired* replica names, not currently-running
  containers** — deliberately. Querying running containers raced with
  container start-up and dropped just-created replicas from the snapshot;
  STRICT_DNS lets Envoy resolve each name as it comes up.
- **Reconcile** (`service.reconcile`) is the core loop: list containers labeled
  `easydeploy.service=<name>`, keyed by the `easydeploy.replica` index, then
  create/start/remove to match `replicas`. `Create`/`Scale`/`Redeploy`/`Delete`
  all reconcile then `registry.Sync`. `SyncAll` runs on startup.
- **Editing** (`service.Manager.Update`, `PUT /api/services/{name}`): changes any
  editable field (image, env, ports, resources, scaling, git, advanced) then
  **rolling-replaces** replicas (remove+recreate each 0..N-1, then drop leftover
  indices if the count shrank). Identity fields (name, owner, webhook token,
  environment) are preserved. The `/services` responses are `serviceView`s that
  include the **parsed `advanced`** spec so the edit form round-trips it
  (`advToForm` in `ServiceAdvanced.tsx` inverts `buildAdvanced`). Member quota is
  re-checked via `OwnerUsageExcludingService` so a service isn't counted against
  itself. `ServiceCard` has an **Edit** button; `ServiceEditor` takes an optional
  `editing` service (name field locked).
- **Autoscaling** (`service.RunAutoscaler`, started from `main`, 20s tick):
  averages replica CPU via `docker.CPUPercent` (one-shot stats delta) and steps
  replicas ±1 with hysteresis (up above target, down below target/2), clamped to
  [min,max]. Idle services fall to `minReplicas` — expected.
- **Full Docker options** (`docker.AdvancedSpec` in `docker/spec.go`): the
  service form exposes the breadth of `docker run` — extra published ports,
  bind/volume/tmpfs mounts, command/entrypoint, user/hostname/workdir, restart
  policy, capabilities, privileged/read-only-rootfs/init, DNS, devices,
  sysctls, tmpfs, PIDs/swap/cpu-shares, stop signal/timeout, log driver, custom
  labels, and healthcheck. `AdvancedSpec` is **embedded** in `docker.DeploySpec`
  and applied by `applyAdvanced` (additive — zero values keep Docker defaults).
  It is persisted per-service as JSON in the `services.advanced` column and
  re-applied to every replica in `service.replicaSpec`. To add another option:
  add the field to `AdvancedSpec`, map it in `applyAdvanced`, and add a control
  to `web/src/ServiceAdvanced.tsx` (the form uses an edit-friendly `AdvForm`
  that `buildAdvanced` converts to the API shape).
  - **Published host ports are offset per replica** (`offsetHostPorts` in
    `service.go`): a fixed host port can bind only one container, so replica *i*
    gets `hostPort+i` (base → replica 0, base+1 → replica 1, …). Without this the
    2nd+ replicas fail with "port is already allocated" and sit in `created`.
    Ephemeral ports (empty host port) are untouched. Existing broken replicas
    only pick up the fix on **Redeploy** (reconcile's Start can't re-map a
    container's ports; recreate does).
- **Git webhook** (`POST /api/hooks/{token}`, public, outside the JWT
  middleware, authed by the unguessable token): `service.BuildAndRedeploy`
  clones the repo with **go-git**, `docker.BuildImage` builds+tags
  `easydeploy/<svc>:latest` from its Dockerfile, then `Redeploy` rolls replicas
  one at a time. Runs in a background goroutine; the handler returns 202.

**xDS version gotcha (already fixed, don't regress):** `xds.NewManager` seeds
the snapshot version from `time.Now().UnixMilli()`, not 0. A long-running Envoy
remembers the last version it ACKed; if the server restarts and its counter
resets to a lower number, Envoy treats new snapshots as **stale and silently
ignores them** (you see `cds.update_success` stuck and clusters missing). The
monotonic seed keeps versions increasing across restarts.

## Conventions

- Log/stats streaming uses WebSockets (`api/handlers.go`). Docker's multiplexed
  log stream is demuxed with `stdcopy.StdCopy` into text frames; `closeOnDisconnect`
  closes the docker stream when the client goes away to unblock the copy loop.
- **Volume management** (`docker/volumes.go`, admin-only routes): create,
  delete (with `?force` for in-use volumes), inspect, and a full file manager —
  **browse, mkdir, upload, download, delete**.
  - **Size + ref-count are loaded lazily.** `GET /volumes` returns the list
    instantly with `size:-1`; the client then fetches `GET /volumes/usage`
    (which runs `cli.DiskUsage`) in the background and fills the rows. Crucially
    `VolumeUsage` scopes DiskUsage to `types.VolumeObject` — the unfiltered call
    also computes every image/container/build-cache size, which is very slow on
    a busy host (this made the remote Volumes tab crawl).
  - File ops go through a **cached helper container per volume**
    (`easydeploy-volhelper-<vol>`, labeled `easydeploy.volhelper=true`), an
    `alpine sleep infinity` with the volume mounted at `/mnt`. `volumeHelper`
    reuses it (survives restarts by name), so only the first op pays
    container-create cost (~3s) — subsequent ops are `exec`/archive calls
    (~60ms). Browse/mkdir/delete are `execCapture`; upload is
    `CopyToContainer` (a one-file tar), download is `CopyFromContainer`
    (extract the single tar entry). Helpers are reaped on volume delete and on
    server shutdown (`CleanupVolumeHelpers`).
  - **Path safety**: `safePath` = `volMount + path.Clean("/"+sub)` so a path
    can never escape `/mnt`; root maps to `/mnt` exactly so delete refuses it.
  - Download uses a `?token=` URL (the auth middleware accepts it) so a plain
    `<a download>` works without an Authorization header.
- **Interactive shell** (`handleExec` + `docker.Exec`): a TTY exec bridged over a
  WebSocket. With a TTY the stream is raw (no stdcopy). Protocol: client→server
  **binary** frames are stdin, **text** frames are JSON `{cols,rows}` resize
  control; server→client binary frames are terminal output. The browser side is
  xterm.js (`web/src/Terminal.tsx`). The output pump `conn.Close()`s on shell EOF
  so the client sees the session end.
- **Monitoring** (`web/src/Monitor.tsx`) consumes the existing `/stats` WS and
  computes CPU% (cpu/system delta × online CPUs), memory used-vs-limit, and
  network rates client-side, rendered as threshold-colored meters + single-hue
  sparklines. No new backend endpoint — it's a consumer of `/stats`.
- **Service detail** (`ServiceDetail` in `App.tsx`): clicking a service card's
  header opens a topology view — **domains → load balancer (Envoy round-robin) →
  replica containers** — plus a config grid. It fetches `api.containers()` to
  show each replica's live state; a replica opens the full `ContainerDetail`
  (rendered as a sibling overlay, not nested, so its clicks don't bubble to the
  service modal). `ServiceCard` gained an `onOpen` (header button) alongside
  `onEdit`.
- **Container detail** (`ContainerDetail` in `App.tsx`): clicking a container row
  opens a tabbed modal — **Overview** (inspect: image, limits, networks, ports,
  env, labels), **Logs**, **Monitor**, **Shell**. `LogViewer`/`Monitor`/`Shell`
  each take an `embedded` prop that returns just their inner panel (no modal
  chrome) so the detail can host them; only the active tab is mounted, so its WS
  connects lazily and closes on tab switch. The parent tracks `detailId` and
  re-derives the container from the reloaded list, so lifecycle actions refresh
  the view and a removed container auto-closes it.
- The frontend has no data-fetching library — a small `useAsync` hook in
  `App.tsx` and the typed client in `api.ts` are the whole pattern. Match it
  rather than introducing React Query etc.
- **List search + pagination** is a reusable hook `useSearchPage(items, match,
  pageSize)` in `App.tsx` with `SearchInput` + `Pager` components. `match(item,
  q)` takes the already-lowercased query; module-level `matchContainer` /
  `matchNetwork` / `matchVolume` / `matchService` define per-list fields. Call
  the hook **before** the loading/error early-returns (rules of hooks) with
  `list ?? []`; render `sp.pageItems`, and show `Pager` (auto-hides at ≤1 page).
  Applied to Containers/Networks/Volumes (tables) and Services (cards).
- Persisted state is intentionally minimal: routes, services, tunnels,
  registries, users, resource requests. The Docker daemon remains the source of
  truth for live container state.
- The frontend gates on a token in `localStorage` (`api.ts` `auth`); a 401 from
  any request clears it and drops back to the login screen via
  `setUnauthorizedHandler`.
- **URL routing is a tiny hash router** (`route.ts`): the fragment encodes the
  active tab and selected environment (`#/volumes?env=3`) so a refresh — or a
  shared link — restores both. Hash routing is deliberate (the fragment never
  reaches the server, so it needs no SPA path-fallback config). `Dashboard`
  owns the tab; the `environment` store (`api.ts`) mirrors the env id into the
  hash and follows it back on `hashchange`.
- **Shared view primitives** (`App.tsx`): list/grid views render `TableSkeleton`
  / `CardSkeleton` (shimmer placeholders) while loading — not a spinner; `Err`
  is a full panel with a **Try again** button (pass `onRetry={reload}`); `Empty`
  wraps its icon in `.empty-icon` and takes an optional `action`. The design
  system is token-driven (`styles.css` `:root`: layered `--bg/--panel/--panel-2`
  surfaces, `--shadow-1..3`, `--ring`); keyboard focus uses `:focus-visible` +
  `--ring` everywhere. Upload progress and the sidebar `.health-chip` (live
  connection state) round out the "trust" cues.
- The default landing tab is **Overview** (`Overview` component): environment-
  aware stat tiles (running/stopped/services/networks/volumes/images for the
  selected env), an **Environments** health panel (per-env up/down + version,
  click to switch), and a "needs attention" panel (pending requests, stopped
  containers). Its tiles fetch through the same endpoint header, so they reflect
  the selected environment. Gated fetches (volumes/endpoints) use `[isAdmin]`
  deps so they refetch once the role loads.
- The UI is a responsive **sidebar dashboard** (`.shell` = CSS grid: sidebar +
  main-col with topbar). Below 900px the sidebar becomes an off-canvas drawer
  (`.drawer-open` + `.scrim`); tables are wrapped in `.table-wrap` for
  horizontal scroll and table action cells are `nowrap` so rows stay short.
  `useAsync` coerces a `null` list (Go marshals empty slices as `null`) to `[]`
  so empty views don't spin forever. WebSocket components guard a `closed` flag
  so StrictMode's double-mount doesn't surface a spurious error.
