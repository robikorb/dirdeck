#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"

docker_desktop_bin=/Applications/Docker.app/Contents/Resources/bin
if [ -d "$docker_desktop_bin" ]; then
  PATH="$PATH:$docker_desktop_bin"
  export PATH
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "Tracked files contain local changes. Commit or stash them before updating." >&2
  exit 1
fi

echo "Creating pre-update backup..."
./scripts/backup.sh

echo "Updating source..."
git pull --ff-only

echo "Rebuilding and recreating the container..."
docker compose up -d --build --remove-orphans

attempt=0
until docker compose exec -T file-manager \
  wget -qO- http://127.0.0.1:8080/api/ready >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo "Update finished, but the application did not become ready." >&2
    echo "Use the backup paths printed above and see docs/UPGRADING.md." >&2
    exit 1
  fi
  sleep 2
done

echo "Update complete. Persistent settings and mounted user files were preserved."
