#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"

docker_desktop_bin=/Applications/Docker.app/Contents/Resources/bin
if [ -d "$docker_desktop_bin" ]; then
  PATH="$PATH:$docker_desktop_bin"
  export PATH
fi

umask 077
mkdir -p backups
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
state_volume=$(sed -n 's/^LGFM_DATA_VOLUME=//p' .env 2>/dev/null | tail -n 1)
state_volume=${state_volume:-liquid-glass-file-manager_app-state}
state_archive="$project_dir/backups/lgfm-state-$timestamp.tar.gz"
config_archive="$project_dir/backups/lgfm-config-$timestamp.tar.gz"

was_running=false
if [ -n "$(docker compose ps -q file-manager 2>/dev/null)" ]; then
  was_running=true
  docker compose stop file-manager >/dev/null
fi

restart_app() {
  if [ "$was_running" = true ]; then
    docker compose start file-manager >/dev/null 2>&1 || true
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

retention=$(sed -n 's/^LGFM_BACKUP_RETENTION=//p' .env 2>/dev/null | tail -n 1)
retention=${retention:-10}
case "$retention" in
  ''|*[!0-9]*) retention=10 ;;
esac
if [ "$retention" -gt 0 ]; then
  ls -1t "$project_dir"/backups/lgfm-state-*.tar.gz 2>/dev/null |
    awk -v keep="$retention" 'NR > keep' |
    while IFS= read -r old_state; do
      old_config=$(printf '%s\n' "$old_state" | sed 's#/lgfm-state-#/lgfm-config-#')
      rm -f -- "$old_state" "$old_config"
    done
fi

restart_app
trap - EXIT INT TERM

echo "$state_archive"
echo "$config_archive"
