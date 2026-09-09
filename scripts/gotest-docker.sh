#!/usr/bin/env bash
# Run Go tests inside a golang container. The host toolchain crashes in
# gojieba's cgo init (MinGW UCRT vs gojieba), so the container is the
# reliable local runner for any package importing internal/types.
# weknora-gotest:latest additionally carries libsqlite3-dev for the
# sqlite-vec cgo dependency (build it once with:
#   docker run --name p golang:1.26 bash -c 'apt-get update -qq && apt-get install -y -qq libsqlite3-dev' \
#     && docker commit p weknora-gotest:latest && docker rm p).
# Usage: scripts/gotest-docker.sh ./internal/application/service/learning/ [extra go test args...]
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"
modcache="$(go env GOMODCACHE)"
image="weknora-gotest:latest"
if ! docker image inspect "$image" >/dev/null 2>&1; then
  image="golang:1.26"
fi
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "${repo}:/workspace" -w //workspace \
  -e GOMODCACHE=/gomodcache -v "${modcache}:/gomodcache" \
  "$image" go test "$@" 2>&1
