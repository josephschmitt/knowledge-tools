#!/usr/bin/env bash
# Build once so the site is live the moment the container is ready (a failed FIRST build hard-fails
# the container, so a broken vault surfaces in the deploy), then hand off to the long-running
# server. The server rebuilds on an authenticated POST /rebuild; later rebuild failures keep the
# last good site served rather than taking the container down.
set -euo pipefail

/opt/site/build.sh
exec node /opt/site/serve.mjs
