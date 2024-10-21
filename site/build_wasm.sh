#!/usr/bin/env bash

set -eux

cd "$(dirname "$0")"

echo "WARNING: make sure you're using 'nix develop' for the correct go version"

GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ./public/main.wasm ../cmd/wasm
wasm-opt -Oz ./public/main.wasm -o ./public/main.wasm --enable-bulk-memory

cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" ./public/wasm_exec.js
