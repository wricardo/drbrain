#!/bin/sh
# Roll a release back on the host: point current at a previous release dir and
# restart the one unit. The env file is untouched.
#
#   ./deploy/rollback.sh <release-name>     e.g. ./deploy/rollback.sh 96ca5a1
#
# List candidates with: ls -1 /opt/drbrain/releases
set -eu
release="${1:?usage: rollback.sh <release-name>}"
target="/opt/drbrain/releases/$release"
[ -x "$target/drbrain" ] || { echo "rollback: $target has no drbrain binary" >&2; exit 1; }
ln -sfn "$target" /opt/drbrain/current.new
mv -Tf /opt/drbrain/current.new /opt/drbrain/current
systemctl restart drbrain.service
systemctl is-active drbrain.service
/opt/drbrain/current/deploy/smoke.sh
