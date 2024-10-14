#!/usr/bin/env bash

set -eux

cd "$(dirname "$0")"

GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ./app/assets/main.wasm ../cmd/wasm
wasm-opt -Oz ./app/assets/main.wasm -o ./app/assets/main.wasm --enable-bulk-memory
