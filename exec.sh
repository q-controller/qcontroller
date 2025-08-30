#!/usr/bin/env bash

set -Eeuo pipefail

trap cleanup SIGINT SIGTERM ERR EXIT

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)

cleanup() {
    local exit_code=$?
    exit "$exit_code"
}

BUILD_DIR=${script_dir}/build
GEN_DIR=${script_dir}/src/generated
CACHE_DIR=/tmp/qcontroller-cache

mkdir -p ${CACHE_DIR}/.go
mkdir -p ${CACHE_DIR}/.go-mod-cache
mkdir -p ${CACHE_DIR}/.buf

if OS_TYPE="$(uname -s)" && [[ "$OS_TYPE" == "Linux" ]]; then
    GOOS=linux
else
    GOOS=darwin
fi

docker run --rm -it -v "${script_dir}:${script_dir}" \
    -v "${CACHE_DIR}/.go:${CACHE_DIR}/.go" \
    -v "${CACHE_DIR}/.go-mod-cache:${CACHE_DIR}/.go-mod-cache" \
    -v "${CACHE_DIR}/.buf:${CACHE_DIR}/.buf" \
    --workdir ${script_dir} \
    -e BUF_CACHE_DIR=${CACHE_DIR}/.buf \
    -e GOCACHE=${CACHE_DIR}/.go \
    -e GOMODCACHE=${CACHE_DIR}/.go-mod-cache \
    -e GOOS=${GOOS} \
    $(docker build -q --target pre-build . -f Dockerfile --build-arg USER_ID=$(id -u) --build-arg GROUP_ID=$(id -g)) \
    $@
