#!/usr/bin/env bash
# Builds the browser version into web/: ikml.wasm plus Go's wasm_exec.js.
set -euo pipefail
cd "$(dirname "$0")/.."
v=$(tr -d '[:space:]' < VERSION)
GOOS=js GOARCH=wasm go build -trimpath -ldflags "-s -w -X main.Version=$v" -o web/ikml.wasm ./wasm
root=$(go env GOROOT)
if [ -f "$root/lib/wasm/wasm_exec.js" ]; then cp "$root/lib/wasm/wasm_exec.js" web/; else cp "$root/misc/wasm/wasm_exec.js" web/; fi
ls -l web/ikml.wasm web/wasm_exec.js
