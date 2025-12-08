#!/usr/bin/env bash

set -Eeuo pipefail
trap cleanup SIGINT SIGTERM ERR EXIT

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)
TEMP_DIR="$(mktemp -d)"

die () {
    local msg=$1
    local code=${2-1}
    echo "$msg"
    exit "$code"
}

cleanup() {
    local exit_code=$?
    trap - SIGINT SIGTERM ERR EXIT
    echo "Cleaning up..."
    rm -rf "$TEMP_DIR"
    exit "$exit_code"
}

function build_binaries() {
    BIN_DIR="$1"
    mkdir -p "${BIN_DIR}"

    UNAME_S="$(uname -s)"
    if [[ "$UNAME_S" == "Linux" ]]; then
        go build -o "${BIN_DIR}/qcontrollerd" src/qcontrollerd/main.go
    elif [[ "$UNAME_S" == "Darwin" ]]; then
        # On macOS, build universal binary for amd64 and arm64
        ARCHS="amd64 arm64"
        BINS=""
        for ARCH in $ARCHS; do
            echo "Building for Darwin/$ARCH..."
            GOARCH=$ARCH go build -o "${TEMP_DIR}/qcontrollerd-$ARCH" src/qcontrollerd/main.go
            BINS="${BINS} ${TEMP_DIR}/qcontrollerd-$ARCH"
        done
        lipo -create -output "${BIN_DIR}/qcontrollerd" ${BINS}
        xattr -d com.apple.quarantine "${BIN_DIR}/qcontrollerd" || true
        chmod 755 "${BIN_DIR}/qcontrollerd"
    else
        die "Unsupported OS: $UNAME_S"
    fi
}

build_binaries "${1:-${script_dir}/build}"
