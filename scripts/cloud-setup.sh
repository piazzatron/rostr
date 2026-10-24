#!/bin/bash
# The setup script for rostr's Claude Code cloud environment. Set the environment's setup script to:
#
#   bash rostr/scripts/cloud-setup.sh
#
# It installs the Go modules, the site's packages, and Codex, starts Docker, and builds the images tests/run.sh uses.
# Containers can't use the HTTPS proxy a session's commands go through, so the session's own builds have to come from
# Docker's cache; one that needs to download something (after a go.mod change, say) fails until the next session.
# A session's VM can also restart partway through with nothing running, so this adds a SessionStart hook that starts
# Docker again.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)

for module in cli server tests; do
  (cd "$repo/$module" && go mod download)
done
# npm ci, since an older npm's `npm install` rewrites the lockfile
(cd "$repo/site" && npm ci --no-audit --no-fund --loglevel=error)
# The Codex version rostr's Codex hooks were last checked against (docs/integrations-research.md)
npm install -g --no-audit --no-fund --loglevel=error @openai/codex@0.164.0

start_docker='docker info >/dev/null 2>&1 || (nohup dockerd >/var/log/dockerd.log 2>&1 &)'
eval "$start_docker"
for _ in $(seq 30); do
  docker info >/dev/null 2>&1 && break
  sleep 1
done
if ! (cd "$repo" && docker compose build --quiet && docker compose pull --quiet postgres); then
  echo "cloud-setup: couldn't build the Docker images ahead of time, so tests/run.sh can't build them in a session" >&2
fi

# Start Docker on every session start and resume, alongside whatever hooks are already there
python3 - "$start_docker" <<'PY'
import json, os, sys
path, command = os.path.expanduser("~/.claude/settings.json"), sys.argv[1]
settings = json.load(open(path)) if os.path.exists(path) else {}
groups = settings.setdefault("hooks", {}).setdefault("SessionStart", [])
if not any(h.get("command") == command for g in groups for h in g.get("hooks", [])):
    groups.append({"hooks": [{"type": "command", "command": command}]})
    os.makedirs(os.path.dirname(path), exist_ok=True)
    json.dump(settings, open(path, "w"), indent=2)
PY
