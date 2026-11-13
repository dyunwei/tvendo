#!/bin/sh
set -eu
version=$(node scripts/release-metadata.mjs --version)
if [ "$#" -gt 0 ] && [ "$1" != "$version" ]; then
 echo "指定版本必须与 VERSION ($version) 一致" >&2; exit 2
fi
output=${2:-distribution/dist}
case "$output" in /*) ;; *) output="$(pwd)/$output" ;; esac
mkdir -p "$output"
for os in linux darwin windows; do
 for arch in amd64 arm64; do
  suffix=; [ "$os" != windows ] || suffix=.exe
  (cd backend && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$version" -o "$output/tvendo-$os-$arch$suffix" ./cmd/tvendo)
 done
done
node distribution/package.mjs "$output" "$version"

node scripts/verify-cli.mjs "$output"
