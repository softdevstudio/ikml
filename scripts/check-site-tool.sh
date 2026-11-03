#!/usr/bin/env bash
# Checks that the site's copy of the browser tool (static/tool/ in the
# photozeitgeist/ikml.com repo) is the one in a release's ikml-web.zip.
#   scripts/check-site-tool.sh SITE_DIR [TAG]
# TAG defaults to the latest release. Needs gh (the site repo is private, so
# this runs from a session that can read both repos, not from CI).
set -euo pipefail
site=${1:?usage: check-site-tool.sh SITE_DIR [TAG]}
tag=${2:-$(gh api repos/softdevstudio/ikml/releases/latest --jq .tag_name)}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
id=$(gh api "repos/softdevstudio/ikml/releases/tags/$tag" --jq '.assets[] | select(.name == "ikml-web.zip") | .id')
[ -n "$id" ] || { echo "::error::$tag has no ikml-web.zip"; exit 1; }
gh api -H "Accept: application/octet-stream" "repos/softdevstudio/ikml/releases/assets/$id" > "$tmp/web.zip"
mkdir "$tmp/web" && (cd "$tmp/web" && unzip -q ../web.zip)
bad=0
# The tool's files; the site keeps the licence as LICENSE.txt and has its
# own page instead of index.html.
for f in ikml.js ikml.wasm wasm_exec.js; do
  if ! cmp -s "$tmp/web/$f" "$site/static/tool/$f"; then
    echo "::error::static/tool/$f differs from $f in $tag's ikml-web.zip"; bad=1
  fi
done
if ! cmp -s "$tmp/web/LICENSE" "$site/static/tool/LICENSE.txt"; then
  echo "::error::static/tool/LICENSE.txt differs from LICENSE in $tag's ikml-web.zip"; bad=1
fi
[ $bad = 0 ] && echo "static/tool matches $tag's ikml-web.zip"
exit $bad
