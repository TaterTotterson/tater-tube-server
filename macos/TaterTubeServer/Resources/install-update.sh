#!/bin/sh
set -eu

APP_PID="$1"
NEW_APP="$2"
TARGET_APP="$3"
LOG_PATH="$4"
RELAUNCH_COMMAND="${5:-/usr/bin/open}"
SCRIPT_PATH="$0"

mkdir -p "$(dirname "$LOG_PATH")"
exec >>"$LOG_PATH" 2>&1

log() {
  printf '%s %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"
}

TARGET_PARENT="$(dirname "$TARGET_APP")"
TARGET_NAME="$(basename "$TARGET_APP")"
STAGED="${TARGET_PARENT}/.${TARGET_NAME}.updating"
BACKUP="${TARGET_PARENT}/.${TARGET_NAME}.previous"
NEW_PARENT="$(dirname "$NEW_APP")"
INSTALL_COMPLETE=0

finish() {
  status="$?"
  trap - EXIT HUP INT TERM
  if [ "$INSTALL_COMPLETE" -ne 1 ]; then
    log "Installer failed with status $status."
    rm -rf "$STAGED"
    if [ ! -d "$TARGET_APP" ] && [ -d "$BACKUP" ]; then
      mv "$BACKUP" "$TARGET_APP"
      "$RELAUNCH_COMMAND" -n "$TARGET_APP" >/dev/null 2>&1 || true
    fi
  fi
  exit "$status"
}
trap finish EXIT HUP INT TERM

log "Waiting for Tater Tube Server process $APP_PID to exit."
WAIT_COUNT=0
while kill -0 "$APP_PID" 2>/dev/null && [ "$WAIT_COUNT" -lt 300 ]; do
  sleep 0.2
  WAIT_COUNT=$((WAIT_COUNT + 1))
done
if kill -0 "$APP_PID" 2>/dev/null; then
  log "Application process $APP_PID did not exit within 60 seconds."
  exit 1
fi

log "Installing update into $TARGET_APP."
rm -rf "$STAGED" "$BACKUP"
/usr/bin/ditto "$NEW_APP" "$STAGED"
if [ -d "$TARGET_APP" ]; then
  mv "$TARGET_APP" "$BACKUP"
fi
mv "$STAGED" "$TARGET_APP"
/usr/bin/xattr -dr com.apple.quarantine "$TARGET_APP" 2>/dev/null || true

if ! "$RELAUNCH_COMMAND" -n "$TARGET_APP"; then
  log "The updated application did not relaunch; restoring the previous version."
  rm -rf "$TARGET_APP"
  if [ -d "$BACKUP" ]; then
    mv "$BACKUP" "$TARGET_APP"
    "$RELAUNCH_COMMAND" -n "$TARGET_APP" >/dev/null 2>&1 || true
  fi
  exit 1
fi

INSTALL_COMPLETE=1
trap - EXIT HUP INT TERM
log "Update installed and relaunched successfully."
rm -rf "$NEW_PARENT" "$BACKUP"
rm -f "$SCRIPT_PATH"
