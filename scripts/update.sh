#!/bin/sh
set -eu

# Update DirDeck in place, whichever way it was installed.
#
#   ./scripts/update.sh              update to the newest published version
#   ./scripts/update.sh 0.2.0-rc.6   update to an exact version (image stack)
#
# The two stacks update differently and must not be mixed: see scripts/lib-stack.sh.

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"

docker_desktop_bin=/Applications/Docker.app/Contents/Resources/bin
if [ -d "$docker_desktop_bin" ]; then
  PATH="$PATH:$docker_desktop_bin"
  export PATH
fi

. "$project_dir/scripts/lib-stack.sh"
detect_stack

requested_version=${1:-}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$1"
  else
    return 1
  fi
}

# GitHub returns releases newest first. Prefer the newest stable release; while
# the project is still in candidates there is none, so fall back to the newest
# prerelease and say so rather than silently doing nothing.
resolve_newest_version() {
  releases=$(fetch "https://api.github.com/repos/robikorb/dirdeck/releases?per_page=30") || return 1
  list=$(printf '%s' "$releases" | tr '{' '\n' | awk '
    /"tag_name"/ {
      tag = ""
      if (match($0, /"tag_name"[[:space:]]*:[[:space:]]*"[^"]+"/)) {
        t = substr($0, RSTART, RLENGTH)
        sub(/.*:[[:space:]]*"/, "", t)
        sub(/"$/, "", t)
        tag = t
      }
      pre = ($0 ~ /"prerelease"[[:space:]]*:[[:space:]]*true/) ? "true" : "false"
      if (tag != "") print pre, tag
    }')
  [ -n "$list" ] || return 1
  stable=$(printf '%s\n' "$list" | awk '$1 == "false" { print $2; exit }')
  if [ -n "$stable" ]; then
    printf '%s\n' "${stable#v}"
    return 0
  fi
  newest=$(printf '%s\n' "$list" | awk 'NR == 1 { print $2 }')
  [ -n "$newest" ] || return 1
  echo "No stable release yet; using the newest candidate." >&2
  printf '%s\n' "${newest#v}"
}

current_version() {
  docker compose $COMPOSE_FILES ps -q "$APP_SERVICE" 2>/dev/null |
    head -n 1 |
    xargs -r docker inspect --format '{{.Config.Image}}' 2>/dev/null |
    sed 's/.*://'
}

# ---------------------------------------------------------------------------
# Image stack: no Git checkout required, no compile, no rebuild.
# ---------------------------------------------------------------------------
update_image_stack() {
  version=$requested_version
  if [ -z "$version" ]; then
    echo "Looking up the newest published version..."
    version=$(resolve_newest_version) || {
      echo "Could not reach the GitHub releases API." >&2
      echo "Pass the version explicitly: ./scripts/update.sh <version>" >&2
      exit 1
    }
  fi
  version=${version#v}

  running=$(current_version)
  if [ -n "$running" ] && [ "$running" = "$version" ]; then
    echo "Already running $version. Nothing to do."
    exit 0
  fi

  echo "Creating pre-update backup..."
  ./scripts/backup.sh

  # The version goes in .env, not into the command. compose.yml still pins the
  # version it shipped with, so passing it inline would work once and then let
  # the next plain `docker compose up -d` quietly recreate the container on the
  # older image.
  if [ -f .env ] && grep -q '^DIRDECK_VERSION=' .env; then
    tmp=$(mktemp)
    sed "s/^DIRDECK_VERSION=.*/DIRDECK_VERSION=$version/" .env > "$tmp"
    cat "$tmp" > .env
    rm -f "$tmp"
  else
    printf 'DIRDECK_VERSION=%s\n' "$version" >> .env
  fi

  echo "Pulling $version..."
  DIRDECK_VERSION=$version docker compose $COMPOSE_FILES pull
  echo "Recreating the container..."
  DIRDECK_VERSION=$version docker compose $COMPOSE_FILES up -d --remove-orphans
}

# ---------------------------------------------------------------------------
# Source stack: fast-forward the checkout and rebuild.
# ---------------------------------------------------------------------------
update_source_stack() {
  if [ -n "$requested_version" ]; then
    echo "This is a source install; it tracks the Git branch rather than a" >&2
    echo "published version. Check out the tag yourself, then run this again." >&2
    exit 1
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
  docker compose $COMPOSE_FILES up -d --build --remove-orphans
}

echo "Detected the $STACK stack."
if [ "$STACK" = image ]; then
  update_image_stack
else
  update_source_stack
fi

attempt=0
until docker compose $COMPOSE_FILES exec -T "$APP_SERVICE" \
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
