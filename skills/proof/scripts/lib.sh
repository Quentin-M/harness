# shellcheck shell=bash disable=SC2034
# Sourced by every proof script. Pins tool versions and keeps caches under $STATE.
set -euo pipefail

STATE=${HARNESS_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/harness}
PW_VERSION=1.64.0
MMDC_VERSION=12.0.0
VERIFY_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

export npm_config_cache=$STATE/npm-cache
export npm_config_update_notifier=false
export PLAYWRIGHT_BROWSERS_PATH=$STATE/playwright/browsers
export PUPPETEER_SKIP_DOWNLOAD=1
export PUPPETEER_CACHE_DIR=$STATE/puppeteer

die() { echo "$(basename "$0"): $*" >&2; exit 1; }

# Prints the script's header comment block (lines after the shebang up to the first blank line).
usage() { sed -n '2,/^$/{/^#/s/^# \{0,1\}//p;}' "$0"; exit "${1:-0}"; }

need() { command -v "$1" >/dev/null || { echo "$(basename "$0"): $1 not found; install: $2" >&2; exit 2; }; }

# Installs the pinned playwright package and chromium under $STATE once, then exports NODE_PATH.
pw_setup() {
  need node "brew install node"
  local prefix=$STATE/playwright/npm have
  have=$(node -p "require('$prefix/node_modules/playwright/package.json').version" 2>/dev/null || true)
  if [[ $have != "$PW_VERSION" ]]; then
    npm install --silent --no-audit --no-fund --prefix "$prefix" "playwright@$PW_VERSION" >&2
  fi
  "$prefix/node_modules/.bin/playwright" install chromium >&2
  export NODE_PATH=$prefix/node_modules
}

pw() { pw_setup; node "$VERIFY_DIR/pw.cjs" "$@"; }

abspath() { local d; d=$(cd "$(dirname "$1")" && pwd) && echo "$d/$(basename "$1")"; }
