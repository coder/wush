#!/usr/bin/env bash

set -eux

cd "$(dirname "$0")"

GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ./app/assets/main.wasm ../cmd/wasm
wasm-opt -Oz ./app/assets/main.wasm -o ./app/assets/main.wasm --enable-bulk-memory

pnpm build

gsutil -h "Content-Type:application/wasm" \
       -h "Content-Encoding:gzip" \
       -h "Cache-Control:public,max-age=31536000,immutable" \
       cp ./build/client/assets/main-*.wasm.gz gs://wush-assets-prod/assets/
rm ./build/client/assets/main-*.wasm*

wrangler pages deploy ./build/client
