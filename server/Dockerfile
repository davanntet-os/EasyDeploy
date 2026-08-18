# Build context is the repository ROOT (see deploy/docker-compose.yml), so
# this Dockerfile can build both the web UI and the Go server and ship them
# in one image.
#
# The SQLite driver is pure Go (modernc.org/sqlite), so the server builds
# fully static with CGO disabled into a minimal distroless image.

# 1. Build the frontend.
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web/ ./
RUN npm run build

# 2. Build the server.
FROM golang:1.26 AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/easydeploy ./cmd/easydeploy

# 3. Final image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/easydeploy /usr/local/bin/easydeploy
COPY --from=web /web/dist /web
ENV EASYDEPLOY_HTTP_ADDR=":8080" \
    EASYDEPLOY_XDS_ADDR=":18000" \
    EASYDEPLOY_WEB_DIR="/web"
EXPOSE 8080 18000
ENTRYPOINT ["/usr/local/bin/easydeploy"]
