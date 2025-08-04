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
pids=()

OS_TYPE="$(uname -s)"

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)

usage() {
    cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-h] --rundir PATH --bin PATH [--interface NAME] [--cidr VALUE]

Starts qcontrollerd

Available options:

-h, --help      Print this help and exit
--bin           Path to qcontrollerd binary
--rundir        Path to the rundir
--interface     Interface name [default: ${INTERFACE_NAME}] (Linux only)
--cidr          Subnet [default: ${SUBNET}] (Linux only)
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
                if [[ "$OS_TYPE" == "Linux" ]]; then
                    INTERFACE_NAME="${2-}"
                fi
                shift
                ;;
            --cidr)
                if [[ "$OS_TYPE" == "Linux" ]]; then
                    SUBNET="${2-}"
                fi
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

if [[ "$OS_TYPE" == "Linux" ]]; then
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
else
cat >${CONFIGDIR}/qemu-config.json <<EOF
{
    "port": "${QEMU_PORT}"
}
EOF
fi

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
sudo -v
sudo ${QCONTROLLERD} qemu -c ${CONFIGDIR}/qemu-config.json >${LOGDIR}/qemu.out 2>${LOGDIR}/qemu.err &
pids+=($!)

# Wait until qemu is ready
sleep 1

${QCONTROLLERD} gateway -c ${CONFIGDIR}/gateway-config.json >${LOGDIR}/gateway.out 2>${LOGDIR}/gateway.err &
pids+=($!)

# Wait until gateway is ready
sleep 1

${QCONTROLLERD} controller -c ${CONFIGDIR}/controller-config.json >${LOGDIR}/controller.out 2>${LOGDIR}/controller.err &
pids+=($!)

# Wait until controller is ready
sleep 1

# Wait for the first one to finish
while :; do
    for pid in "${pids[@]}"; do
        if ! kill -0 "$pid" 2>/dev/null; then
            wait "$pid"
            echo "Process $pid finished"
            exit 0
        fi
    done
    sleep 0.1
done
