# EasyDeploy developer tasks.
.PHONY: dev-server dev-web postgres build build-web test vet tidy envoy up down fmt

# Start a local Postgres for development.
postgres:
	docker run -d --name easydeploy-postgres --network easydeploy-edge \
		-e POSTGRES_USER=easydeploy -e POSTGRES_PASSWORD=easydeploy -e POSTGRES_DB=easydeploy \
		-p 5432:5432 postgres:16-alpine

# Run the Go API + xDS control plane against the local Docker daemon and
# Postgres. ADMIN_PASSWORD and SECRET_KEY are required.
dev-server:
	cd server && \
	EASYDEPLOY_DATABASE_URL="postgres://easydeploy:easydeploy@localhost:5432/easydeploy?sslmode=disable" \
	EASYDEPLOY_ADMIN_PASSWORD="$${EASYDEPLOY_ADMIN_PASSWORD:-admin123}" \
	EASYDEPLOY_SECRET_KEY="$${EASYDEPLOY_SECRET_KEY:-dev-secret-key-change-me}" \
	go run ./cmd/easydeploy

# Run the Vite dev server (proxies /api to the Go server).
dev-web:
	cd web && npm run dev

# Build the server binary.
build:
	cd server && go build -o easydeploy ./cmd/easydeploy

build-web:
	cd web && npm install && npm run build

test:
	cd server && go test ./...

vet:
	cd server && go vet ./...

tidy:
	cd server && go mod tidy

fmt:
	cd server && gofmt -w .

# Run Envoy locally in Docker, pointed at the host control plane.
envoy:
	docker run --rm -p 80:10000 -p 9901:9901 \
		--add-host host.docker.internal:host-gateway \
		-v $(PWD)/deploy/envoy.yaml:/etc/envoy/envoy.yaml:ro \
		envoyproxy/envoy:v1.31-latest \
		-c /etc/envoy/envoy.yaml --service-node easydeploy-envoy

# Full stack via docker compose.
up:
	docker compose -f deploy/docker-compose.yml up --build

down:
	docker compose -f deploy/docker-compose.yml down
