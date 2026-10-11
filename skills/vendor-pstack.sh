#!/usr/bin/env bash
# Copy cursor/plugins pstack skills into skills/ at a pinned SHA.
# Usage: skills/vendor-pstack.sh [sha]   (default: upstream main HEAD)
# Owns only the skills listed in skills/PSTACK.md plus poteto-mode/references/agents/.
set -euo pipefail

REPO=https://github.com/cursor/plugins
SKILLS=$(cd "$(dirname "$0")" && pwd)
SHEET=$SKILLS/PSTACK.md
EXCLUDED=(make-bot-ui)

SHA=${1:-$(git ls-remote "$REPO" refs/heads/main | cut -f1)}
[[ $SHA =~ ^[0-9a-f]{40}$ ]] || { echo "need a full 40-char sha, got: $SHA" >&2; exit 1; }

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
git -C "$TMP" init -q
git -C "$TMP" remote add origin "$REPO"
git -C "$TMP" sparse-checkout set --no-cone /pstack/
git -C "$TMP" fetch -q --depth 1 --filter=blob:none origin "$SHA"
git -C "$TMP" checkout -q FETCH_HEAD
SRC=$TMP/pstack
[[ -d $SRC/skills ]] || { echo "pstack/skills missing at $SHA" >&2; exit 1; }

VERSION=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' \
  "$SRC/.cursor-plugin/plugin.json" 2>/dev/null || echo unknown)
DATE=$(git -C "$TMP" show -s --format=%cI FETCH_HEAD)

owned=$(sed -n 's/^  - //p' "$SHEET")
new=()
for d in "$SRC"/skills/*/; do
  n=$(basename "$d")
  [[ " ${EXCLUDED[*]} " == *" $n "* ]] && continue
  if [[ -e $SKILLS/$n ]] && ! grep -qx "$n" <<<"$owned"; then
    echo "upstream skill '$n' collides with our own skills/$n; rename ours first" >&2; exit 1
  fi
  new+=("$n")
done

for n in $owned; do
  [[ " ${new[*]} " == *" $n "* ]] || rm -r "${SKILLS:?}/$n"
done
for n in "${new[@]}"; do
  rsync -a --delete --exclude /references/agents "$SRC/skills/$n/" "$SKILLS/$n/"
done
mkdir -p "$SKILLS/poteto-mode/references/agents"
rsync -a --delete "$SRC/agents/" "$SKILLS/poteto-mode/references/agents/"
cp "$SRC/LICENSE" "$SKILLS/LICENSE-pstack"
# local edits to vendored skills survive updates as patches
for p in "$SKILLS"/pstack.patches/*.patch; do
  [[ -e $p ]] && git -C "$SKILLS/.." apply "$p"
done

{
  sed '/^```yaml$/q' "$SHEET"
  echo "repo: $REPO"
  echo "path: pstack/"
  echo "sha: $SHA"
  echo "date: $DATE"
  echo "version: $VERSION"
  echo "skills:"
  printf '  - %s\n' "${new[@]}"
  echo '```'
} >"$TMP/sheet" && mv "$TMP/sheet" "$SHEET"

echo "pstack $VERSION @ $SHA -> skills (${#new[@]} skills)"
