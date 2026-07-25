#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$project_dir"

# Non-interactive macOS shells do not always inherit Docker Desktop's CLI
# directory. It contains both docker and the credential helper needed to pull
# public images.
docker_desktop_bin=/Applications/Docker.app/Contents/Resources/bin
if [ -d "$docker_desktop_bin" ]; then
  PATH="$PATH:$docker_desktop_bin"
  export PATH
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required: https://docs.docker.com/engine/install/" >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose v2 is required." >&2
  exit 1
fi

umask 077
mkdir -p secrets config backups

if [ ! -f .env ]; then
  cp .env.example .env
  if command -v id >/dev/null 2>&1; then
    runtime_uid=$(id -u)
    runtime_gid=$(id -g)
    if [ "$runtime_uid" -gt 0 ] && [ "$runtime_gid" -gt 0 ]; then
      sed "s/^PUID=.*/PUID=$runtime_uid/; s/^PGID=.*/PGID=$runtime_gid/" .env > .env.tmp
      mv .env.tmp .env
    fi
  fi
  echo "Created .env"
fi

if [ ! -f config/volumes.yaml ]; then
  cp config/volumes.example.yaml config/volumes.yaml
  echo "Created config/volumes.yaml with disposable fixtures."
fi
if [ ! -f compose.override.yml ]; then
  cp compose.override.example.yml compose.override.yml
  echo "Created compose.override.yml for host-specific storage mounts."
fi

reuse_secrets=false
if [ -s secrets/admin_username ] && [ -s secrets/admin_password ]; then
  printf "Existing admin credentials found. Reuse them? [Y/n] "
  read -r reuse_answer
  case "$reuse_answer" in
    n|N|no|NO) ;;
    *) reuse_secrets=true ;;
  esac
fi

if [ "$reuse_secrets" = false ]; then
  printf "Admin username [admin]: "
  read -r admin_username
  admin_username=${admin_username:-admin}
  if [ -t 0 ]; then
    printf "Admin password (minimum 12 characters): "
    stty -echo
    trap 'stty echo 2>/dev/null || true' EXIT INT TERM
    read -r admin_password
    stty echo
    trap - EXIT INT TERM
    printf "\nConfirm admin password: "
    stty -echo
    trap 'stty echo 2>/dev/null || true' EXIT INT TERM
    read -r admin_password_confirm
    stty echo
    trap - EXIT INT TERM
    printf "\n"
  else
    echo "Interactive terminal required to create admin credentials." >&2
    exit 1
  fi
  if [ "${#admin_password}" -lt 12 ]; then
    echo "Password must contain at least 12 characters." >&2
    exit 1
  fi
  if [ "$admin_password" != "$admin_password_confirm" ]; then
    echo "Passwords do not match." >&2
    exit 1
  fi
  printf '%s\n' "$admin_username" > secrets/admin_username
  printf '%s\n' "$admin_password" > secrets/admin_password
  chmod 600 secrets/admin_username secrets/admin_password
  unset admin_password admin_password_confirm
  echo "Created local admin secret files."
fi

docker compose config >/dev/null
docker compose up -d --build

attempt=0
until docker compose exec -T file-manager \
  wget -qO- http://127.0.0.1:8080/api/ready >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo "The container started but did not become ready. Run:" >&2
    echo "  docker compose logs file-manager" >&2
    exit 1
  fi
  sleep 2
done

port=$(sed -n 's/^DIRDECK_PORT=//p' .env | tail -n 1)
port=${port:-3002}
echo ""
echo "DirDeck is ready: http://127.0.0.1:$port"
echo "Add real storage to compose.override.yml and config/volumes.yaml."
