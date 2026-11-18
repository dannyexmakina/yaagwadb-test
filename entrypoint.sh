#!/bin/sh
set -eu

# daemon・mount・Apache は www-data で実行する
run_as="setpriv --reuid=www-data --regid=www-data --init-groups"
config=/etc/yaagwadb/config.toml
mount_point=/var/www/html/wp-content/uploads
# mount の終了を待つ上限（0.2 秒単位の回数）。アンマウント後も終了しない場合があるため、超えた場合は強制終了する
mount_stop_attempts=25

mkdir -p "$mount_point"
chown www-data:www-data /var/lib/yaagwadb /var/www/html /var/www/html/wp-content "$mount_point"

$run_as yaagwadb -c "$config" daemon &
daemon=$!

attempt=0
until $run_as yaagwadb -c "$config" status >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 50 ] || ! kill -0 "$daemon" 2>/dev/null; then
    echo "the daemon did not start" >&2
    kill -INT "$daemon" 2>/dev/null || true
    exit 1
  fi
  sleep 0.2
done

$run_as yaagwadb -c "$config" mount &
mount=$!

# WordPress 本体の展開と Apache の起動は公式イメージの entrypoint に任せる
# Apache は終了時にプロセスグループ全体へ SIGTERM を送るため、setsid で別のプロセスグループにする
setsid docker-entrypoint.sh apache2-foreground &
apache=$!

trap 'kill -TERM "$apache" 2>/dev/null || true' INT TERM
wait "$apache" || true

kill -INT "$mount" 2>/dev/null || true
attempt=0
while kill -0 "$mount" 2>/dev/null && [ "$attempt" -lt "$mount_stop_attempts" ]; do
  attempt=$((attempt + 1))
  sleep 0.2
done
if kill -0 "$mount" 2>/dev/null; then
  echo "the mount did not stop; killing it" >&2
  kill -KILL "$mount" 2>/dev/null || true
fi
wait "$mount" || true

kill -INT "$daemon" 2>/dev/null || true
wait "$daemon" || true
