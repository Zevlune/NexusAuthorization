#!/bin/sh
set -eu

data_dir=/app/data
app_user=nexus-authorization

# Already non-root (for example `docker run --user`): run as that user.
if [ "$(id -u)" != "0" ]; then
    exec /app/nexus-authorization "$@"
fi

mkdir -p "$data_dir"
# Some bind mounts (NAS, rootless or NFS storage) reject chown; fall back to the
# existing permissions and fail only if the service user cannot write there.
if ! chown -R "$app_user:$app_user" "$data_dir" 2>/dev/null; then
    echo "warning: cannot change ownership of $data_dir; using existing permissions" >&2
fi
chmod 700 "$data_dir" 2>/dev/null || true
if ! su-exec "$app_user:$app_user" test -w "$data_dir"; then
    echo "error: $data_dir is not writable by $app_user; fix the volume permissions" >&2
    exit 1
fi

exec su-exec "$app_user:$app_user" /app/nexus-authorization "$@"
