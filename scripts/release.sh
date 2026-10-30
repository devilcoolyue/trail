#!/usr/bin/env bash
# 编译各平台的 trail 并打包到 dist/：scripts/release.sh v0.1.0
set -euo pipefail
cd "$(dirname "$0")/.."

version=${1:?用法：scripts/release.sh v0.1.0}
plugin_version=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' claude-plugin/.claude-plugin/plugin.json)
if [ "v$plugin_version" != "$version" ]; then
  echo "版本不一致：插件 plugin.json 是 $plugin_version，这次发布是 $version" >&2
  exit 1
fi
if grep -rIl --exclude-dir=dist --exclude-dir=.git --exclude=release.sh --exclude=set-owner.sh OWNER . >/dev/null; then
  echo "还有 OWNER 占位没换，先运行 scripts/set-owner.sh <GitHub 用户名>" >&2
  exit 1
fi

go vet ./...
go test ./...

rm -rf dist && mkdir dist
for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
  os=${target%/*} arch=${target#*/}
  name=trail_${version}_${os}_${arch}
  mkdir -p "dist/$name"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "dist/$name/trail" .
  cp LICENSE README.md "dist/$name/"
  tar -C dist -czf "dist/$name.tar.gz" "$name"
  rm -rf "dist/$name"
done
# macOS 自带的是 shasum
sum() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
(cd dist && sum ./*.tar.gz > SHA256SUMS)
ls -lh dist
