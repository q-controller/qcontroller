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

    curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.7/install.sh | PROFILE="${BASH_ENV}" bash

    . "${BASH_ENV}"
}

cleanup() {
    local exit_code=$?
    exit "$exit_code"
}

"${script_dir}/schema/prepare.sh"

# qcontroller-specific tools (lint, vuln scan).
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
go install github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.6.0

install_nvm
nvm install 26
npm install -g corepack
