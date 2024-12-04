#!/usr/bin/env bash

set -eux

cd "$(dirname "$0")"

mkdir -p wasm

echo "WARNING: make sure you're using 'nix develop' for the correct go version"

GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ./wasm/main.wasm ../cmd/wasm
wasm-opt -Oz ./wasm/main.wasm -o ./wasm/main.wasm --enable-bulk-memory

cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" ./wasm/wasm_exec.js && chmod 644 ./wasm/wasm_exec.js
