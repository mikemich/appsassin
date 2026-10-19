#!/usr/bin/env bash
# Builds a universal (arm64 + x86_64) binary into dist/appsassin-macos.tar.gz.
# Set VERSION (e.g. VERSION=0.2.0) to stamp it into `appsassin version`.
set -euo pipefail
cd "$(dirname "$0")/.."
rm -rf dist && mkdir -p dist
export CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=12.0
ldflags="-s -w"
[ -n "${VERSION:-}" ] && ldflags="$ldflags -X main.version=$VERSION"
for arch in arm64 amd64; do
  carch=$arch; [ "$arch" = amd64 ] && carch=x86_64
  GOARCH=$arch CC="clang -arch $carch" go build -trimpath -ldflags="$ldflags" -o "dist/appsassin-$arch" .
done
lipo -create -output dist/appsassin dist/appsassin-arm64 dist/appsassin-amd64
rm dist/appsassin-arm64 dist/appsassin-amd64
codesign --force --sign - dist/appsassin
tar -C dist -czf dist/appsassin-macos.tar.gz appsassin
(cd dist && shasum -a 256 appsassin-macos.tar.gz) | tee dist/appsassin-macos.tar.gz.sha256
