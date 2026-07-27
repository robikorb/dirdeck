#!/bin/sh
# Shared stack detection for update.sh and backup.sh.
#
# DirDeck ships two Compose stacks and they are not interchangeable:
#
#   compose.yml        image stack    service "dirdeck"       volume dirdeck-data
#   compose.build.yml  source stack   service "file-manager"  volume liquid-glass-file-manager_app-state
#
# A Git checkout can run either one. Somebody who clones the repository and
# follows the README quick start — `docker compose up -d` — is running the image
# stack from inside a checkout, which is the case both scripts used to get wrong:
# they assumed the source stack unconditionally, so backup.sh tried to archive
# config files that only exist in source installs and aborted, and update.sh
# would have rebuilt from source and pointed the app at a different state volume,
# making every setting and the administrator password appear to vanish.
#
# Detection prefers what is actually running over what happens to be on disk.
#
# Sets: STACK, COMPOSE_FILES, APP_SERVICE, STATE_VOLUME_DEFAULT

detect_stack() {
  if [ -n "$(docker compose -f compose.yml ps -q dirdeck 2>/dev/null)" ]; then
    STACK=image
  elif [ -f compose.build.yml ] &&
    [ -n "$(docker compose -f compose.build.yml ps -q file-manager 2>/dev/null)" ]; then
    STACK=source
  elif [ -f compose.build.yml ] && { [ -f compose.override.yml ] || [ -f config/volumes.yaml ]; }; then
    # Nothing running, but the host-specific files only a source install has.
    STACK=source
  else
    STACK=image
  fi

  if [ "$STACK" = source ]; then
    COMPOSE_FILES="-f compose.build.yml"
    if [ -f compose.override.yml ]; then
      COMPOSE_FILES="$COMPOSE_FILES -f compose.override.yml"
    fi
    APP_SERVICE=file-manager
    STATE_VOLUME_DEFAULT=liquid-glass-file-manager_app-state
  else
    COMPOSE_FILES="-f compose.yml"
    APP_SERVICE=dirdeck
    STATE_VOLUME_DEFAULT=dirdeck-data
  fi

  export STACK COMPOSE_FILES APP_SERVICE STATE_VOLUME_DEFAULT
}

# Read a key from .env, current name first, then the pre-rename one.
env_value() {
  sed -n "s/^$1=//p" .env 2>/dev/null | tail -n 1
}

state_volume_name() {
  value=$(env_value DIRDECK_DATA_VOLUME)
  [ -n "$value" ] || value=$(env_value LGFM_DATA_VOLUME)
  printf '%s\n' "${value:-$STATE_VOLUME_DEFAULT}"
}
