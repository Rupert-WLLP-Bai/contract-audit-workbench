# Base images are pinned to multi-arch registry manifest digests (the digest a
# plain `docker pull <name>:<tag>` reports). Re-pin deliberately after verifying
# the new digest against the official registry; never restore a floating tag.
FROM golang:1.26.7-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514 AS go-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY capability-packs/ ./capability-packs/
ARG TARGETOS
ARG TARGETARCH
RUN mkdir -p /out \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/documentservice ./cmd/documentservice \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/auditexecution ./cmd/auditexecution \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/modelgateway ./cmd/modelgateway \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/toolgateway ./cmd/toolgateway \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/healthmonitor ./cmd/healthmonitor

FROM oven/bun:1.4.3@sha256:ec06c3b6cea04192ae6770c434f668ca41d343ad19fa6472216c7b48be39c598 AS web-build
WORKDIR /src/web
COPY web/package.json web/bun.lock web/index.html web/admin.html web/portal.html web/vite.config.ts web/tsconfig.json web/tsconfig.test.json ./
RUN bun install --frozen-lockfile
COPY web/src/ ./src/
COPY web/public/ ./public/
RUN bun run typecheck \
    && bun run build:client \
    && bun run build:admin \
    && bun run build:portal

FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f AS backend
RUN apk add --no-cache ca-certificates tzdata poppler-utils poppler-data \
    && addgroup -S -g 10001 contractaudit \
    && adduser -S -D -u 10001 -G contractaudit contractaudit \
    && install -d -o contractaudit -g contractaudit /var/lib/contract-audit \
    && install -d /run/contract-audit \
    && install -m 0444 /dev/null /run/contract-audit/identity-directory.json
ENV DATA_DIR=/var/lib/contract-audit
WORKDIR /app
COPY --from=go-build --chown=contractaudit:contractaudit /out/server /app/server
COPY --from=go-build --chown=contractaudit:contractaudit /out/gateway /app/gateway
COPY --from=go-build --chown=contractaudit:contractaudit /out/documentservice /app/documentservice
COPY --from=go-build --chown=contractaudit:contractaudit /out/auditexecution /app/auditexecution
COPY --from=go-build --chown=contractaudit:contractaudit /out/worker /app/worker
COPY --from=go-build --chown=contractaudit:contractaudit /out/modelgateway /app/modelgateway
COPY --from=go-build --chown=contractaudit:contractaudit /out/toolgateway /app/toolgateway
COPY --from=go-build --chown=contractaudit:contractaudit /out/migrate /app/migrate
COPY --from=go-build --chown=contractaudit:contractaudit /out/healthmonitor /app/healthmonitor
COPY --chown=contractaudit:contractaudit deploy/app/entrypoint.sh /app/entrypoint
RUN chmod 0555 /app/entrypoint
USER contractaudit:contractaudit
ENTRYPOINT ["/app/entrypoint"]
CMD ["/app/server"]

FROM nginxinc/nginx-unprivileged:1.29-alpine@sha256:0c79d56aee561a1d81c63f00eee5fb5fe29279560cdc55e91425133104c7fbe6 AS client
COPY deploy/app/nginx/backend-proxy.conf /etc/nginx/snippets/backend-proxy.conf
COPY deploy/app/nginx/proxy-headers.conf /etc/nginx/snippets/proxy-headers.conf
COPY deploy/app/nginx/client.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /src/web/dist/client/ /usr/share/nginx/html/

FROM nginxinc/nginx-unprivileged:1.29-alpine@sha256:0c79d56aee561a1d81c63f00eee5fb5fe29279560cdc55e91425133104c7fbe6 AS admin
COPY deploy/app/nginx/backend-proxy.conf /etc/nginx/snippets/backend-proxy.conf
COPY deploy/app/nginx/proxy-headers.conf /etc/nginx/snippets/proxy-headers.conf
COPY deploy/app/nginx/admin.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /src/web/dist/admin/ /usr/share/nginx/html/

FROM nginxinc/nginx-unprivileged:1.29-alpine@sha256:0c79d56aee561a1d81c63f00eee5fb5fe29279560cdc55e91425133104c7fbe6 AS portal
COPY deploy/app/nginx/backend-proxy.conf /etc/nginx/snippets/backend-proxy.conf
COPY deploy/app/nginx/proxy-headers.conf /etc/nginx/snippets/proxy-headers.conf
COPY deploy/app/nginx/portal.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /src/web/dist/portal/ /usr/share/nginx/html/
