#!/usr/bin/env bash
# Builds a universal (arm64 + x86_64) binary into dist/appsassin-macos.tar.gz.
set -euo pipefail
cd "$(dirname "$0")/.."
rm -rf dist && mkdir -p dist
export CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=12.0
for arch in arm64 amd64; do
  carch=$arch; [ "$arch" = amd64 ] && carch=x86_64
  GOARCH=$arch CC="clang -arch $carch" go build -trimpath -ldflags="-s -w" -o "dist/appsassin-$arch" .
done
lipo -create -output dist/appsassin dist/appsassin-arm64 dist/appsassin-amd64
rm dist/appsassin-arm64 dist/appsassin-amd64
codesign --force --sign - dist/appsassin
tar -C dist -czf dist/appsassin-macos.tar.gz appsassin
shasum -a 256 dist/appsassin-macos.tar.gz | tee dist/appsassin-macos.tar.gz.sha256
