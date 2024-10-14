#!/usr/bin/env bash

set -eux

cd "$(dirname "$0")"

./build_wasm.sh

pnpm build

# The wasm artifact is too big uncompressed to serve directly from cf pages, so
# we manually serve the gzipped wasm from google storage. I would use r2 but
# it's broken and won't let us activate.
gsutil -h "Content-Type:application/wasm" \
       -h "Content-Encoding:gzip" \
       -h "Cache-Control:public,max-age=31536000,immutable" \
       cp ./build/client/assets/main-*.wasm.gz gs://wush-assets-prod/assets/
# rm the wasm files so they don't get uploaded to cf pages.
rm ./build/client/assets/main-*.wasm*

wrangler pages deploy ./build/client
