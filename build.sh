#!/usr/bin/env bash
# Build spm4a release binaries into dist/ (default: all OS/arch matrix).
# Usage:
#   ./build.sh                  # full matrix
#   ./build.sh linux amd64      # single platform
#   VERSION=v0.2.0 ./build.sh   # explicit version (default: git describe)
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X spm4a/internal/daemon.Version=${VERSION}"

if [ $# -eq 2 ]; then
  targets=("$1 $2")
else
  targets=(
    "windows amd64"
    "windows arm64"
    "linux amd64"
    "linux arm64"
    "darwin amd64"
    "darwin arm64"
  )
fi

mkdir -p dist
for t in "${targets[@]}"; do
  read -r goos goarch <<<"$t"
  out="dist/spm4a-${goos}-${goarch}"
  [ "$goos" = "windows" ] && out="${out}.exe"
  echo "==> ${goos}/${goarch} -> ${out}"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/spm4a
done

if command -v sha256sum >/dev/null 2>&1; then
  (cd dist && sha256sum spm4a-* > checksums.txt)
elif command -v shasum >/dev/null 2>&1; then
  (cd dist && shasum -a 256 spm4a-* > checksums.txt)
fi

echo "done: version=${VERSION}, artifacts in dist/"
