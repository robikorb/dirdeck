#!/bin/sh
set -eu

runtime_uid="${PUID:-1000}"
runtime_gid="${PGID:-1000}"
runtime_umask="${UMASK:-022}"

case "$runtime_uid" in
  ''|*[!0-9]*)
    echo "PUID must be a positive numeric ID" >&2
    exit 1
    ;;
esac

case "$runtime_gid" in
  ''|*[!0-9]*)
    echo "PGID must be a positive numeric ID" >&2
    exit 1
    ;;
esac

if [ "$runtime_uid" -eq 0 ] || [ "$runtime_gid" -eq 0 ]; then
  echo "Refusing to run the file manager as root" >&2
  exit 1
fi

current_gid="$(id -g lgfm)"
current_uid="$(id -u lgfm)"

if [ "$current_gid" != "$runtime_gid" ]; then
  groupmod -o -g "$runtime_gid" lgfm
fi
if [ "$current_uid" != "$runtime_uid" ]; then
  usermod -o -u "$runtime_uid" lgfm
fi

mkdir -p /var/lib/file-manager
chown lgfm:lgfm /var/lib/file-manager
umask "$runtime_umask"

exec gosu lgfm "$@"
