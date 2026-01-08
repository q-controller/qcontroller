#!/usr/bin/env bash

set -Eeuo pipefail

trap cleanup SIGINT SIGTERM ERR EXIT

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)

function install_nvm() {
    # Check if nvm is already installed
    if command -v nvm >/dev/null 2>&1; then
        echo "NVM is already installed."
    else
        echo "NVM not found. Installing..."

        BASH_ENV=${HOME}/.bash_env
        touch "${BASH_ENV}"
        echo ". ${BASH_ENV}" >> ~/.bashrc

        curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | PROFILE="${BASH_ENV}" bash
    fi
}

cleanup() {
    local exit_code=$?
    exit "$exit_code"
}

# Install tools
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.8
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
go install github.com/bufbuild/buf/cmd/buf@v1.57.0
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.27.2
go install github.com/google/gnostic/cmd/protoc-gen-openapi@v0.7.1
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

install_nvm
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"  # This loads nvm
nvm install 22
npm install -g corepack
