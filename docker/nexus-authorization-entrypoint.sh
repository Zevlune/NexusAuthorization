#!/bin/sh
set -eu

data_dir=/app/data
mkdir -p "$data_dir"
chown -R nexus-authorization:nexus-authorization "$data_dir"
chmod 700 "$data_dir"

exec su-exec nexus-authorization:nexus-authorization /app/nexus-authorization
