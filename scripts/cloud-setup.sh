#!/bin/bash
# Setup for rostr's Claude Code cloud environment: starts Docker (so tests/run.sh and `docker compose up postgres` work
# as they do on a laptop), makes image builds work through the session's HTTPS proxy, and installs the Go modules, the
# site's packages, and Codex. The environment's setup script runs it at the start of each new session:
#
#   bash rostr/scripts/cloud-setup.sh
#
# A session's VM can restart partway through (the session resumes with no processes running), and the setup script
# doesn't run again then, so this also registers services.sh as a Claude Code SessionStart hook, which brings Docker
# back on every start and resume.
set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
# The Codex version rostr's Codex hooks were last checked against (docs/integrations-research.md)
CODEX_VERSION=0.164.0
TOOLS=/root/.rostr-cloud

mkdir -p "$TOOLS"
cp /root/.ccr/ca-bundle.crt "$TOOLS/ca-bundle.crt"

# Containers can't reach the session's proxy, which listens on loopback, and don't trust its CA. A forwarder on the
# Docker bridge's gateway makes it reachable, and a Compose override builds the server image with the CA trusted.
cat >"$TOOLS/forward-proxy.py" <<'PY'
# Forwards host:port on the Docker bridge to the same port on loopback, where the session's HTTPS proxy listens.
import asyncio, sys
host, port = sys.argv[1], int(sys.argv[2])
async def pipe(reader, writer):
    try:
        while data := await reader.read(65536):
            writer.write(data)
            await writer.drain()
    finally:
        writer.close()
async def handle(client_reader, client_writer):
    upstream_reader, upstream_writer = await asyncio.open_connection("127.0.0.1", port)
    await asyncio.gather(pipe(client_reader, upstream_writer), pipe(upstream_reader, client_writer), return_exceptions=True)
async def main():
    server = await asyncio.start_server(handle, host, port)
    async with server:
        await server.serve_forever()
asyncio.run(main())
PY
cat >"$TOOLS/compose.override.yml" <<'YAML'
# server/Dockerfile's build, through the session's proxy (via the forwarder on the Docker bridge) with its CA trusted
services:
  server:
    build:
      additional_contexts:
        proxy_ca: /root/.rostr-cloud
      args:
        HTTPS_PROXY: ${ROSTR_DOCKER_PROXY}
        HTTP_PROXY: ${ROSTR_DOCKER_PROXY}
      dockerfile_inline: |
        FROM golang:1.27-alpine AS build
        COPY --from=proxy_ca ca-bundle.crt /etc/ssl/certs/proxy-ca.crt
        ENV SSL_CERT_FILE=/etc/ssl/certs/proxy-ca.crt
        ARG HTTPS_PROXY HTTP_PROXY
        WORKDIR /src
        COPY go.mod go.sum ./
        RUN go mod download
        COPY . .
        RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /rostr-server .

        FROM alpine:3.22
        RUN adduser -D -H rostr
        USER rostr
        COPY --from=build /rostr-server /usr/local/bin/rostr-server
        ENV PORT=8080
        EXPOSE 8080
        CMD ["rostr-server"]
YAML

# services.sh starts whatever isn't running: Docker, the proxy forwarder, and the compose Postgres the server tests use.
# It's quick when everything's up, and prints nothing, since a SessionStart hook's output goes to the agent.
cat >"$TOOLS/services.sh" <<SH
#!/bin/bash
set -euo pipefail
if ! docker info >/dev/null 2>&1; then
  nohup dockerd >/var/log/dockerd.log 2>&1 &
  for _ in \$(seq 1 30); do
    docker info >/dev/null 2>&1 && break
    sleep 1
  done
fi
docker info >/dev/null 2>&1
proxy_port=\${HTTPS_PROXY##*:}
proxy_port=\${proxy_port%%/*}
bridge=\$(docker network inspect bridge -f '{{range .IPAM.Config}}{{.Gateway}}{{end}}')
if ! python3 -c "import socket; socket.create_connection(('\$bridge', \$proxy_port), 1)" 2>/dev/null; then
  nohup python3 $TOOLS/forward-proxy.py "\$bridge" "\$proxy_port" >$TOOLS/forward-proxy.log 2>&1 &
fi
cat >/etc/profile.d/rostr-cloud.sh <<PROFILE
export COMPOSE_FILE=$REPO/docker-compose.yml:$TOOLS/compose.override.yml
export ROSTR_DOCKER_PROXY=http://\$bridge:\$proxy_port
export TEST_DATABASE_URL='postgres://rostr:rostr@localhost:5432/rostr?sslmode=disable'
PROFILE
. /etc/profile.d/rostr-cloud.sh
cd $REPO && docker compose up -d --wait postgres >/dev/null 2>&1
SH
chmod +x "$TOOLS/services.sh"
grep -q rostr-cloud.sh /root/.bashrc 2>/dev/null || echo '. /etc/profile.d/rostr-cloud.sh' >>/root/.bashrc
"$TOOLS/services.sh"

# Run services.sh on every Claude Code session start and resume, alongside any hooks already there
python3 - "$TOOLS/services.sh" <<'PY'
import json, os, sys
path, command = os.path.expanduser("~/.claude/settings.json"), sys.argv[1]
settings = json.load(open(path)) if os.path.exists(path) else {}
groups = settings.setdefault("hooks", {}).setdefault("SessionStart", [])
if not any(h.get("command") == command for g in groups for h in g.get("hooks", [])):
    groups.append({"hooks": [{"type": "command", "command": command}]})
    os.makedirs(os.path.dirname(path), exist_ok=True)
    json.dump(settings, open(path, "w"), indent=2)
PY

# Go modules, the site's packages (npm ci: an older npm's install rewrites the lockfile), and Codex
for module in cli server tests; do
  (cd "$REPO/$module" && go mod download)
done
(cd "$REPO/site" && npm ci --no-audit --no-fund --loglevel=error)
if [ "$(codex --version 2>/dev/null)" != "codex-cli $CODEX_VERSION" ]; then
  npm install -g --no-audit --no-fund --loglevel=error "@openai/codex@$CODEX_VERSION"
fi
