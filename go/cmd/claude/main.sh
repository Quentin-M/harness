#!/bin/sh
# claude: build the Go launcher into the state dir when its sources change, then exec it.
set -eu
GO=$(cd "$(dirname "$0")/../.." && pwd -P)
ROOT=$(dirname "$GO")
STATE=${HARNESS_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/harness}
BIN=$STATE/bin/harness
if [ ! -x "$BIN" ] || [ -n "$(find "$GO" -type f -newer "$BIN" | head -n 1)" ]; then
  (cd "$GO" && go build -o "$BIN" ./cmd/claude)
fi
HARNESS_ROOT=${HARNESS_ROOT:-$ROOT} exec "$BIN" "$@"
