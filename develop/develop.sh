#!/bin/sh
set -eu

config="$DEVELOP_CONFIGS/develop-${STORAGE}.toml"
binary="$YAAGWADB_BINARY"

mkdir -p "$DEVELOP_MOUNT_POINT"

if [ -n "${DAEMON_NOFILE:-}" ]; then
  # Lower the daemon's open file limit so that load tests can reach it (task run:limited).
  # Both soft and hard are lowered, because Go raises the soft limit to the hard limit at startup
  (ulimit -Sn "$DAEMON_NOFILE" && ulimit -Hn "$DAEMON_NOFILE" && exec "$binary" -c "$config" daemon) &
else
  "$binary" -c "$config" daemon &
fi
daemon=$!

attempt=0
until "$binary" -c "$config" status >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge "$DAEMON_WAIT_ATTEMPTS" ] || ! kill -0 "$daemon" 2>/dev/null; then
    echo "the daemon did not start" >&2
    kill -INT "$daemon" 2>/dev/null || true
    exit 1
  fi
  sleep "$DAEMON_WAIT_INTERVAL"
done

"$binary" -c "$config" hrana &
hrana=$!

# Runs as PID 1 in the container, so it handles Ctrl-C (SIGINT) and docker stop (SIGTERM) itself and forwards them to the child
if [ "$DEVELOP_MOUNT" = "true" ]; then
  "$binary" -c "$config" mount &
  mount=$!
  trap 'kill -INT "$mount" 2>/dev/null || true' INT TERM
  wait "$mount" || true
  kill -INT "$hrana" 2>/dev/null || true
else
  trap 'kill -INT "$hrana" 2>/dev/null || true' INT TERM
fi
wait "$hrana" || true
kill -INT "$daemon" 2>/dev/null || true
wait "$daemon" || true
