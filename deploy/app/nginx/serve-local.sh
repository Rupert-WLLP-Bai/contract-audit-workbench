#!/usr/bin/env bash
# Serve the three built Vite release artifacts with the delivery nginx site
# configs for a local (non-compose) run.
#
# Isolation rules:
#   - every run gets a unique run id; containers are named
#     contract-audit-site-<site>-<run-id> and labelled
#     contract-audit-serve=owned / contract-audit-serve-run=<run-id>;
#   - the exact container ids are recorded in <out-dir>/serve-owners.tsv and
#     serve-manifest.json; --stop removes only ids that are recorded there AND
#     still carry the matching ownership label. A name alone never authorises
#     deletion;
#   - a run refuses to start when <out-dir> already holds files, or when a
#     legacy fixed name (contract-audit-site-<site>) already exists, so an
#     unrelated container or config set is never overwritten or removed.
#
# Network modes (the compose stack resolves `gateway:8080` through Docker DNS):
#   bridge   (default) site configs stay exactly as delivered; only the shared
#            backend line `set $gateway_backend gateway:8080;` is retargeted to
#            the resolved host.docker.internal IPv4, and the sites are published
#            on loopback ports. Used where the edge listens on the host loopback.
#   host-net containers run with --network host; each site config also has its
#            listen line changed to listen 127.0.0.1:<port> and the backend is
#            set to 127.0.0.1:<edge port>. Two documented substitutions per
#            site, not byte-identical.
#
# Usage:
#   deploy/app/nginx/serve-local.sh --edge 127.0.0.1:18080 \
#       [--mode bridge|host-net] \
#       [--client-port 18090] [--admin-port 18091] [--portal-port 18092] \
#       [--dist web/dist] [--out-dir /tmp/contract-audit-sites/<run-id>] \
#       [--nginx-image nginx:alpine] [--route-check]
#   deploy/app/nginx/serve-local.sh --stop --out-dir /tmp/contract-audit-sites/<run-id>
set -eu

root=$(cd "$(dirname "$0")/../../.." && pwd)
nginx_conf_dir="$root/deploy/app/nginx"
sites=(client admin portal)
legacy_names=(contract-audit-site-client contract-audit-site-admin contract-audit-site-portal)
owned_label="contract-audit-serve=owned"

inspect_label() {
    docker inspect --format "{{index .Config.Labels \"$2\"}}" "$1" 2>/dev/null || true
}

stop_owned() {
    out_dir="$1"
    owners="$out_dir/serve-owners.tsv"
    if [ ! -f "$owners" ]; then
        printf 'refusing --stop: %s has no serve-owners.tsv; nothing is deleted by name\n' "$out_dir" >&2
        return 2
    fi
    # Validate every recorded container first, so a tampered record never leaves
    # a partially removed run.
    while IFS=$'\t' read -r run_id site name container_id; do
        [ -n "${container_id:-}" ] || continue
        if ! docker inspect "$container_id" >/dev/null 2>&1; then
            continue
        fi
        owned=$(inspect_label "$container_id" "contract-audit-serve")
        recorded=$(inspect_label "$container_id" "contract-audit-serve-run")
        actual_name=$(docker inspect --format '{{.Name}}' "$container_id" | sed 's|^/||')
        if [ "$owned" != "owned" ] || [ "$recorded" != "$run_id" ] || [ "$actual_name" != "$name" ]; then
            printf 'refusing --stop: %s (%s) does not match the recorded ownership labels or name\n' \
                "$name" "$container_id" >&2
            return 1
        fi
    done < "$owners"
    removed=0
    while IFS=$'\t' read -r run_id site name container_id; do
        [ -n "${container_id:-}" ] || continue
        if ! docker inspect "$container_id" >/dev/null 2>&1; then
            printf 'skip %s (%s): container no longer exists\n' "$name" "$container_id"
            continue
        fi
        docker rm -f "$container_id" >/dev/null
        removed=$((removed + 1))
        printf 'removed %s (%s)\n' "$name" "$container_id"
    done < "$owners"
    printf 'removed %s owned container(s)\n' "$removed"
    return 0
}

if [ "${1:-}" = "--stop" ]; then
    shift
    out_dir=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --out-dir) out_dir="$2"; shift 2 ;;
            *) printf 'unknown argument for --stop: %s\n' "$1" >&2; exit 2 ;;
        esac
    done
    if [ -z "$out_dir" ]; then
        printf '%s\n' 'usage: serve-local.sh --stop --out-dir <run out-dir>' >&2
        exit 2
    fi
    stop_owned "$out_dir"
    exit $?
fi

edge=""
mode="bridge"
client_port=18090
admin_port=18091
portal_port=18092
dist="${DIST_DIR:-$root/web/dist}"
out_dir="${SITES_OUT_DIR:-}"
nginx_image="${NGINX_IMAGE:-nginx:alpine}"
route_check=0
while [ $# -gt 0 ]; do
    case "$1" in
        --edge) edge="$2"; shift 2 ;;
        --mode) mode="$2"; shift 2 ;;
        --client-port) client_port="$2"; shift 2 ;;
        --admin-port) admin_port="$2"; shift 2 ;;
        --portal-port) portal_port="$2"; shift 2 ;;
        --dist) dist="$2"; shift 2 ;;
        --out-dir) out_dir="$2"; shift 2 ;;
        --nginx-image) nginx_image="$2"; shift 2 ;;
        --route-check) route_check=1; shift ;;
        *) printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
    esac
done

if [ -z "$edge" ]; then
    printf '%s\n' 'usage: serve-local.sh --edge <host:port> [--mode bridge|host-net] [--out-dir DIR]' >&2
    exit 2
fi
case "$mode" in
    bridge|host-net) ;;
    *) printf 'unknown mode: %s\n' "$mode" >&2; exit 2 ;;
esac
edge_host=${edge%:*}
edge_port=${edge##*:}
case "$edge_port" in
    ''|*[!0-9]*) printf 'edge must be host:port, got %s\n' "$edge" >&2; exit 2 ;;
esac

for site in "${sites[@]}"; do
    entry=index.html
    [ "$site" = admin ] && entry=admin.html
    [ "$site" = portal ] && entry=portal.html
    if [ ! -f "$dist/$site/$entry" ]; then
        printf 'artifact %s is missing; run `cd web && bun run build` first\n' "$dist/$site/$entry" >&2
        exit 2
    fi
    for legacy in "${legacy_names[@]}"; do
        if docker inspect "$legacy" >/dev/null 2>&1; then
            printf 'refusing to start: legacy container name %s exists and is not owned by this helper\n' "$legacy" >&2
            exit 2
        fi
    done
done
if ! docker image inspect "$nginx_image" >/dev/null 2>&1; then
    printf 'nginx image %s is not present locally; pull it explicitly first (this script never downloads)\n' "$nginx_image" >&2
    exit 2
fi

run_id=$(printf '%s-%04x%04x' "$(date -u +%Y%m%d%H%M%S)" "$RANDOM" "$RANDOM")
if [ -z "$out_dir" ]; then
    out_dir="/tmp/contract-audit-sites/$run_id"
elif [ -n "$(ls -A "$out_dir" 2>/dev/null || true)" ]; then
    printf 'refusing to start: %s already holds files; choose a fresh --out-dir\n' "$out_dir" >&2
    exit 2
fi
mkdir -p "$out_dir"
: > "$out_dir/serve-owners.tsv"

backend=""
if [ "$mode" = "bridge" ]; then
    if [ "$edge_host" = "127.0.0.1" ] || [ "$edge_host" = "localhost" ] || [ "$edge_host" = "::1" ]; then
        resolved=$(docker run --rm "$nginx_image" sh -c "getent ahostsv4 host.docker.internal | awk '{print \$1; exit}'" 2>/dev/null || true)
        if [ -z "$resolved" ]; then
            printf '%s\n' 'could not resolve host.docker.internal for the host-run edge' >&2
            exit 1
        fi
        backend="$resolved:$edge_port"
    else
        backend="$edge_host:$edge_port"
    fi
    sed -E "s|^([[:space:]]*)set \\\$gateway_backend gateway:8080;|\1set \$gateway_backend $backend;|" \
        "$nginx_conf_dir/backend-proxy.conf" > "$out_dir/backend-proxy.conf"
else
    backend="127.0.0.1:$edge_port"
    sed -E "s|^([[:space:]]*)set \\\$gateway_backend gateway:8080;|\1set \$gateway_backend $backend;|" \
        "$nginx_conf_dir/backend-proxy.conf" > "$out_dir/backend-proxy.conf"
fi
if ! grep -q "set \$gateway_backend $backend;" "$out_dir/backend-proxy.conf"; then
    printf '%s\n' 'could not retarget the backend snippet; check the delivery file' >&2
    exit 1
fi

declare -A ports=([client]="$client_port" [admin]="$admin_port" [portal]="$portal_port")
for site in "${sites[@]}"; do
    name="contract-audit-site-$site-$run_id"
    if [ "$mode" = "host-net" ]; then
        sed -E "s|^([[:space:]]*)listen 8080 default_server;|\1listen 127.0.0.1:${ports[$site]} default_server;|" \
            "$nginx_conf_dir/$site.conf" > "$out_dir/$site.conf"
        if ! grep -q "listen 127.0.0.1:${ports[$site]} default_server;" "$out_dir/$site.conf"; then
            printf 'could not substitute the listen port for %s\n' "$site" >&2
            exit 1
        fi
        container_id=$(docker run -d --name "$name" --restart no --network host \
            --label "$owned_label" --label "contract-audit-serve-run=$run_id" --label "contract-audit-serve-site=$site" \
            -v "$out_dir/$site.conf:/etc/nginx/conf.d/default.conf:ro" \
            -v "$out_dir/backend-proxy.conf:/etc/nginx/snippets/backend-proxy.conf:ro" \
            -v "$nginx_conf_dir/proxy-headers.conf:/etc/nginx/snippets/proxy-headers.conf:ro" \
            -v "$dist/$site:/usr/share/nginx/html:ro" \
            "$nginx_image")
    else
        cp "$nginx_conf_dir/$site.conf" "$out_dir/$site.conf"
        container_id=$(docker run -d --name "$name" --restart no \
            --add-host=host.docker.internal:host-gateway \
            --label "$owned_label" --label "contract-audit-serve-run=$run_id" --label "contract-audit-serve-site=$site" \
            -p "127.0.0.1:${ports[$site]}:8080" \
            -v "$out_dir/$site.conf:/etc/nginx/conf.d/default.conf:ro" \
            -v "$out_dir/backend-proxy.conf:/etc/nginx/snippets/backend-proxy.conf:ro" \
            -v "$nginx_conf_dir/proxy-headers.conf:/etc/nginx/snippets/proxy-headers.conf:ro" \
            -v "$dist/$site:/usr/share/nginx/html:ro" \
            "$nginx_image")
    fi
    printf '%s\t%s\t%s\t%s\n' "$run_id" "$site" "$name" "$container_id" >> "$out_dir/serve-owners.tsv"
done

cat > "$out_dir/serve-manifest.json" <<JSON
{
  "run_id": "$run_id",
  "mode": "$mode",
  "edge": "$edge",
  "backend": "$backend",
  "dist": "$dist",
  "ports": {"client": $client_port, "admin": $admin_port, "portal": $portal_port},
  "substitutions": {
    "site_configs": "$([ "$mode" = host-net ] && echo 'listen line rewritten to 127.0.0.1:<port>; delivery file otherwise identical' || echo 'byte-identical to deploy/app/nginx/<site>.conf')",
    "backend_snippet": "one line retargeted from gateway:8080 to $backend"
  },
  "created_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
JSON

printf 'served artifacts from %s (run %s, mode %s)\n' "$dist" "$run_id" "$mode"
printf '  client http://127.0.0.1:%s/\n' "$client_port"
printf '  admin  http://127.0.0.1:%s/admin/\n' "$admin_port"
printf '  portal http://127.0.0.1:%s/portal/\n' "$portal_port"
printf '  backend /api -> %s\n' "$backend"
printf '  owners %s/serve-owners.tsv ; stop with: %s --stop --out-dir %s\n' "$out_dir" "$0" "$out_dir"

ready=0
attempts=0
while [ "$attempts" -lt 30 ]; do
    client_status=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$client_port/" || true)
    admin_status=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$admin_port/admin/" || true)
    portal_status=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$portal_port/portal/" || true)
    if [ "$client_status" = "200" ] && [ "$admin_status" = "200" ] && [ "$portal_status" = "200" ]; then
        ready=1
        break
    fi
    attempts=$((attempts + 1))
    sleep 1
done
if [ "$ready" -ne 1 ]; then
    printf 'sites did not all answer 200 (client=%s admin=%s portal=%s)\n' \
        "${client_status:-none}" "${admin_status:-none}" "${portal_status:-none}" >&2
    exit 1
fi
printf '%s\n' 'all three sites answered 200'

if [ "$route_check" -eq 1 ]; then
    session_status=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$client_port/api/session?audience=client" || true)
    openapi=$(curl -s "http://127.0.0.1:$client_port/openapi.json" || true)
    printf 'route check: /api/session -> %s ; /openapi.json -> %s bytes\n' "$session_status" "${#openapi}"
    case "$session_status" in
        200|401|403) ;;
        *) printf 'route check failed: /api/session returned %s through the site proxy\n' "$session_status" >&2; exit 1 ;;
    esac
    case "$openapi" in
        *'"openapi"'*) ;;
        *) printf '%s\n' 'route check failed: /openapi.json did not return an OpenAPI document through the site proxy' >&2; exit 1 ;;
    esac
    printf '%s\n' 'route check passed: the site proxy reaches the real edge'
fi
