# Multi-stage build for DirDeck
# Targets: linux/amd64 and linux/arm64

FROM node:22-bookworm AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.24-bookworm AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN rm -rf ./cmd/server/static && mkdir -p ./cmd/server/static
COPY --from=frontend /src/frontend/dist/ ./cmd/server/static/
RUN CGO_ENABLED=1 go build -o /out/dirdeck -ldflags="-s -w" ./cmd/server

FROM debian:bookworm-slim AS runtime

LABEL org.opencontainers.image.title="DirDeck" \
      org.opencontainers.image.description="Self-hosted dual-pane file manager for homelabs" \
      org.opencontainers.image.source="https://github.com/robikorb/dirdeck" \
      org.opencontainers.image.documentation="https://github.com/robikorb/dirdeck#readme" \
      org.opencontainers.image.licenses="MIT"

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates wget libsqlite3-0 gosu \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd --gid 1000 dirdeck \
  && useradd --uid 1000 --gid 1000 --home-dir /var/lib/file-manager --no-create-home dirdeck \
  && mkdir -p /var/lib/file-manager /etc/dirdeck /mnt/volumes \
  && chown -R dirdeck:dirdeck /var/lib/file-manager

COPY --from=backend /out/dirdeck /usr/local/bin/dirdeck
COPY docker/entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh
WORKDIR /var/lib/file-manager
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/dirdeck"]
