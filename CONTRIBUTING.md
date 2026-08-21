# Contributing to EasyDeploy

Thanks for your interest in improving EasyDeploy! This guide covers how to get a
development environment running, the conventions the codebase follows, and how
to submit a change.

By participating, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to contribute

- **Report a bug** — open an issue with the "Bug report" template.
- **Request a feature** — open an issue with the "Feature request" template.
- **Send a pull request** — fixes, features, docs, or tests are all welcome.
- **Report a security issue** — please follow the [Security Policy](SECURITY.md)
  instead of opening a public issue.

## Development setup

You need **Go 1.26+**, **Node 20+**, a running **Docker daemon**, and
**PostgreSQL** (a container is fine).

```bash
# 1. Start a local Postgres (once)
make postgres

# 2. Run the API server (needs Docker + Postgres).
#    make dev-server supplies dev defaults for the required env vars.
make dev-server

# 3. In another terminal, run the web dev server (proxies /api to :8080).
make dev-web
```

The server refuses to start unless `EASYDEPLOY_ADMIN_PASSWORD` and
`EASYDEPLOY_SECRET_KEY` are set — `make dev-server` sets dev values for you. If
your API runs on a non-default port, point the web proxy at it with
`EASYDEPLOY_API_TARGET` (e.g. `EASYDEPLOY_API_TARGET=http://localhost:8090`).

To run the full stack (server + Envoy + Postgres) the way it ships:

```bash
export EASYDEPLOY_ADMIN_PASSWORD=... EASYDEPLOY_SECRET_KEY=...
docker compose -f deploy/docker-compose.yml up --build
```

## Before you open a pull request

Please make sure the project still builds and vets cleanly.

**Backend (`server/`):**

```bash
make vet                 # go vet ./...
cd server && go build ./...
cd server && go test ./...   # run tests where they exist
```

**Frontend (`web/`):**

```bash
cd web && npm run build  # tsc -b && vite build — the build fails on type errors
```

There is not yet a broad automated test suite, so please **manually verify** the
behavior you changed (run the app and exercise the flow) and describe how you
tested it in the pull request.

## Conventions

- **Match the surrounding code.** Follow the existing style, naming, and comment
  density rather than introducing a new pattern.
- **Do not bump the Docker SDK.** `go.mod` deliberately pins
  `github.com/docker/docker v27.3.1+incompatible` and
  `github.com/docker/go-connections v0.5.0`. Newer versions rename the types
  this code uses and break the build — see the note in `CLAUDE.md`.
- **The frontend has no data-fetching library.** Use the small `useAsync` hook
  and the typed client in `web/src/api.ts`; don't add React Query etc.
- **Keep persistence minimal.** The Docker daemon is the source of truth for
  live container state; only routes, services, tunnels, registries, users,
  requests, and quota accounting are persisted.
- **Migrations are additive.** Add columns/tables with
  `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` / `CREATE TABLE IF NOT EXISTS` in
  `store.migrate` so existing databases upgrade in place.

## Commit messages & pull requests

- Write a concise, imperative summary line (e.g. "Add storage quota for volumes")
  and explain the *why* in the body when it isn't obvious.
- Keep a pull request focused on one change. Reference any issue it closes.
- Fill in the pull request template, including how you tested the change.

## License

By contributing, you agree that your contributions will be licensed under the
project's [Apache License 2.0](LICENSE).
