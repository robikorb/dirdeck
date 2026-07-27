#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"

docker_desktop_bin=/Applications/Docker.app/Contents/Resources/bin
if [ -d "$docker_desktop_bin" ]; then
  PATH="$PATH:$docker_desktop_bin"
  export PATH
fi

. "$project_dir/scripts/lib-stack.sh"
detect_stack

umask 077
mkdir -p backups
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
# Read the current name first, then the pre-rename one, then the default for
# whichever stack is actually running. Installations created before the DirDeck
# rename still pin LGFM_DATA_VOLUME, and the two stacks have different defaults,
# so falling through to a fixed name would archive the wrong volume.
state_volume=$(state_volume_name)

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
if [ -n "$(docker compose $COMPOSE_FILES ps -q "$APP_SERVICE" 2>/dev/null)" ]; then
  was_running=true
  docker compose $COMPOSE_FILES stop "$APP_SERVICE" >/dev/null
fi

restart_app() {
  if [ "$was_running" = true ]; then
    docker compose $COMPOSE_FILES start "$APP_SERVICE" >/dev/null 2>&1 || true
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

# Only source installs have an override, a volume registry, and bootstrap secret
# files. An image install has none of them, and listing a missing path makes tar
# exit non-zero — which used to abort the whole update before it started.
config_files=""
for candidate in .env compose.override.yml config/volumes.yaml \
  secrets/admin_username secrets/admin_password; do
  [ -e "$candidate" ] && config_files="$config_files $candidate"
done

if [ -n "$config_files" ]; then
  # shellcheck disable=SC2086
  LC_ALL=C tar -czf "$config_archive" $config_files
  chmod 600 "$config_archive"
else
  config_archive="(no configuration files to archive)"
fi

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
