#!/usr/bin/env bash

set -Eeuo pipefail

trap cleanup SIGINT SIGTERM ERR EXIT

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)

function install_nvm() {
    for dir in "${NVM_DIR:-$HOME/.nvm}" /home/runner/.nvm /root/.nvm; do
        if [ -s "$dir/nvm.sh" ]; then
            export NVM_DIR="$dir"
            . "$dir/nvm.sh"
            echo "NVM already installed at $dir"
            return
        fi
    done

    echo "NVM not found. Installing..."

    BASH_ENV=${HOME}/.bash_env
    touch "${BASH_ENV}"
    echo ". ${BASH_ENV}" >> ~/.bashrc

    curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | PROFILE="${BASH_ENV}" bash

    . "${BASH_ENV}"
}

cleanup() {
    local exit_code=$?
    exit "$exit_code"
}

"${script_dir}/schema/prepare.sh"

# qcontroller-specific tools (lint, vuln scan).
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0
go install golang.org/x/vuln/cmd/govulncheck@latest

install_nvm
nvm install 22
npm install -g corepack
