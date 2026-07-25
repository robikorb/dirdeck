#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"

docker_desktop_bin=/Applications/Docker.app/Contents/Resources/bin
if [ -d "$docker_desktop_bin" ]; then
  PATH="$PATH:$docker_desktop_bin"
  export PATH
fi

# Source installs combine the build stack with the local host-specific override.
# The default compose.yml is the standalone image-based stack and must not be
# merged with an override that targets the source service name.
COMPOSE_FILES="-f compose.build.yml"
if [ -f compose.override.yml ]; then
  COMPOSE_FILES="$COMPOSE_FILES -f compose.override.yml"
fi

umask 077
mkdir -p backups
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
# Read the current name first, then the pre-rename one. Installations created
# before the DirDeck rename still pin LGFM_DATA_VOLUME, and silently falling
# through to the default would archive the wrong volume.
env_value() {
  sed -n "s/^$1=//p" .env 2>/dev/null | tail -n 1
}
state_volume=$(env_value DIRDECK_DATA_VOLUME)
[ -n "$state_volume" ] || state_volume=$(env_value LGFM_DATA_VOLUME)
state_volume=${state_volume:-dirdeck-data}

# `docker run -v name:/state` creates a missing volume instead of failing, which
# would turn a wrong or misspelled name into a silent, empty "successful" backup.
if ! docker volume inspect "$state_volume" >/dev/null 2>&1; then
  echo "State volume '$state_volume' does not exist." >&2
  echo "Refusing to write an empty backup. Check DIRDECK_DATA_VOLUME in .env" >&2
  echo "against the existing volumes:" >&2
  docker volume ls --format '  {{.Name}}' >&2
  exit 1
fi

state_archive="$project_dir/backups/dirdeck-state-$timestamp.tar.gz"
config_archive="$project_dir/backups/dirdeck-config-$timestamp.tar.gz"

was_running=false
if [ -n "$(docker compose $COMPOSE_FILES ps -q file-manager 2>/dev/null)" ]; then
  was_running=true
  docker compose $COMPOSE_FILES stop file-manager >/dev/null
fi

restart_app() {
  if [ "$was_running" = true ]; then
    docker compose $COMPOSE_FILES start file-manager >/dev/null 2>&1 || true
  fi
}
trap restart_app EXIT INT TERM

docker run --rm \
  -e LANG=C \
  -e LC_ALL=C \
  -v "$state_volume:/state:ro" \
  -v "$project_dir/backups:/backup" \
  alpine:3.22 \
  sh -c 'tar -czf "$1" -C /state . && chmod 600 "$1"' sh \
  "/backup/$(basename "$state_archive")"

LC_ALL=C tar -czf "$config_archive" \
  .env \
  compose.override.yml \
  config/volumes.yaml \
  secrets/admin_username \
  secrets/admin_password
chmod 600 "$config_archive"

retention=$(env_value DIRDECK_BACKUP_RETENTION)
[ -n "$retention" ] || retention=$(env_value LGFM_BACKUP_RETENTION)
retention=${retention:-10}
case "$retention" in
  ''|*[!0-9]*) retention=10 ;;
esac
if [ "$retention" -gt 0 ]; then
  ls -1t "$project_dir"/backups/dirdeck-state-*.tar.gz "$project_dir"/backups/lgfm-state-*.tar.gz 2>/dev/null |
    awk -v keep="$retention" 'NR > keep' |
    while IFS= read -r old_state; do
      old_config=$(printf '%s\n' "$old_state" | sed 's#-state-#-config-#')
      rm -f -- "$old_state" "$old_config"
    done
fi

restart_app
trap - EXIT INT TERM

echo "$state_archive"
echo "$config_archive"
