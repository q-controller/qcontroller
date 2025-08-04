#!/usr/bin/env bash

set -Eeuo pipefail

trap cleanup SIGINT SIGTERM ERR EXIT

INTERFACE_NAME=br0
SUBNET=192.168.71.0/24
CONTROLLER_PORT=8009
QEMU_PORT=8008
GATEWAY_PORT=8080
QCONTROLLERD=""
RUNDIR=""

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)

usage() {
  cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-h] --rundir PATH --bin PATH [--interface NAME] [--cidr VALUE]

Starts qcontrollerd

Available options:

-h, --help      Print this help and exit
--bin           Path to qcontrollerd binary
--rundir        Path to the rundir
--interface     Interface name [defrault: ${INTERFACE_NAME}]
--cidr          Subnet [default: ${SUBNET}]
EOF
  exit
}

cleanup() {
    local exit_code=$?
    trap - SIGINT SIGTERM ERR EXIT
    echo "Cleaning up..."
    # Make sure to exit all child processes
    kill -- -$$
    exit "$exit_code"
}

die() {
    local msg=$1
    local code=${2-1}
    echo "$msg"
    exit "$code"
}

parse_params() {
    while :; do
        case "${1-}" in
            -h | --help) usage ;;
            --interface)
                INTERFACE_NAME="${2-}"
                shift
                ;;
            --bin)
                QCONTROLLERD="${2-}"
                shift
                ;;
            --rundir)
                RUNDIR="${2-}"
                shift
                ;;
            -?*) die "Unknown option: $1" ;;
            *) break ;;
        esac
        shift
    done

    args=("$@")

    # check required params and arguments
    [[ -z "${RUNDIR-}" ]] && die "Missing required parameter: rundir"
    [[ -z "${QCONTROLLERD-}" ]] && die "Missing required parameter: bin"

    return 0
}

parse_params "$@"

LOGDIR=${RUNDIR}/logs
ROOTDIR=${RUNDIR}/root
CONFIGDIR=${RUNDIR}/configs

mkdir -p ${CONFIGDIR}
mkdir -p ${ROOTDIR}
mkdir -p ${LOGDIR}

cat >${CONFIGDIR}/qemu-config.json <<EOF
{
    "port": "${QEMU_PORT}",
    "linuxSettings": {
        "bridge": {
            "name": "${INTERFACE_NAME}",
            "subnet": "${SUBNET}"
        }
    }
}
EOF

cat >${CONFIGDIR}/controller-config.json <<EOF
{
    "port": ${CONTROLLER_PORT},
    "cache": {
        "root": "cache"
    },
    "root": "${ROOTDIR}",
    "qemuEndpoint": "localhost:${QEMU_PORT}"
}
EOF

cat >${CONFIGDIR}/gateway-config.json <<EOF
{
    "port": ${GATEWAY_PORT},
    "controllerEndpoint": "localhost:${CONTROLLER_PORT}",
    "exposeSwaggerUi": true
}
EOF

touch ${LOGDIR}/qemu.out
touch ${LOGDIR}/qemu.err
sudo ${QCONTROLLERD} qemu -c ${CONFIGDIR}/qemu-config.json >${LOGDIR}/qemu.out 2>${LOGDIR}/qemu.err &

# Wait until qemu is ready
sleep 1

${QCONTROLLERD} gateway -c ${CONFIGDIR}/gateway-config.json >${LOGDIR}/gateway.out 2>${LOGDIR}/gateway.err &
GATEWAY_PID=$!

# Wait until gateway is ready
sleep 1

${QCONTROLLERD} controller -c ${CONFIGDIR}/controller-config.json >${LOGDIR}/controller.out 2>${LOGDIR}/controller.err &
CONTROLLER_PID=$!

# Wait until controller is ready
sleep 1

# Wait for the first one to fail and then stop everything
wait -n
