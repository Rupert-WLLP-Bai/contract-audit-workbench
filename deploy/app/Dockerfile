FROM golang:1.26.7-bookworm AS go-build

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

FROM oven/bun:1.4.3 AS web-build
WORKDIR /src/web
COPY web/package.json web/bun.lock web/index.html web/admin.html web/portal.html web/vite.config.ts web/tsconfig.json web/tsconfig.test.json ./
RUN bun install --frozen-lockfile
COPY web/src/ ./src/
COPY web/public/ ./public/
RUN bun run typecheck \
    && bun run build:client \
    && bun run build:admin \
    && bun run build:portal

FROM alpine:3.22 AS backend
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

FROM nginxinc/nginx-unprivileged:1.29-alpine AS client
COPY deploy/app/nginx/backend-proxy.conf /etc/nginx/snippets/backend-proxy.conf
COPY deploy/app/nginx/proxy-headers.conf /etc/nginx/snippets/proxy-headers.conf
COPY deploy/app/nginx/client.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /src/web/dist/client/ /usr/share/nginx/html/

FROM nginxinc/nginx-unprivileged:1.29-alpine AS admin
COPY deploy/app/nginx/backend-proxy.conf /etc/nginx/snippets/backend-proxy.conf
COPY deploy/app/nginx/proxy-headers.conf /etc/nginx/snippets/proxy-headers.conf
COPY deploy/app/nginx/admin.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /src/web/dist/admin/ /usr/share/nginx/html/

FROM nginxinc/nginx-unprivileged:1.29-alpine AS portal
COPY deploy/app/nginx/backend-proxy.conf /etc/nginx/snippets/backend-proxy.conf
COPY deploy/app/nginx/proxy-headers.conf /etc/nginx/snippets/proxy-headers.conf
COPY deploy/app/nginx/portal.conf /etc/nginx/conf.d/default.conf
COPY --from=web-build /src/web/dist/portal/ /usr/share/nginx/html/
