#!/usr/bin/env bash

set -Eeuo pipefail

trap cleanup SIGINT SIGTERM ERR EXIT

export LOG_LEVEL=info

INTERFACE_NAME=br0
HOST_IP=192.168.71.1/24
BRIDGE_IP=192.168.71.3/24
START=192.168.71.4/24
END=192.168.71.254/24
MACOS_MODE=MODE_SHARED
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
Usage: $(basename "${BASH_SOURCE[0]}") [-h] --rundir PATH --bin PATH [--interface NAME] [--cidr VALUE] [--start IP] [--end IP]

Starts qcontrollerd

Available options:

-h, --help         Print this help and exit
--bin              Path to qcontrollerd binary
--rundir           Path to the rundir
--interface        Interface name [default: ${INTERFACE_NAME}] (Linux: bridge name, macOS: not used in shared mode)
--cidr             Gateway CIDR [default: ${HOST_IP}] (Linux only)
--start            Start of DHCP/IP range in CIDR notation [default: ${START}] (both platforms)
--end              End of DHCP/IP range in CIDR notation [default: ${END}] (both platforms)
--macos-mode       macOS network mode: MODE_BRIDGED or MODE_SHARED [default: ${MACOS_MODE}]
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
            --cidr)
                if [[ "$OS_TYPE" == "Linux" ]]; then
                    GATEWAY="${2-}"
                fi
                shift
                ;;
            --start)
                START="${2-}"
                shift
                ;;
            --end)
                END="${2-}"
                shift
                ;;
            --macos-mode)
                MACOS_MODE="${2-}"
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
        "network": {
            "name": "${INTERFACE_NAME}",
            "gateway_ip": "${HOST_IP}",
            "bridge_ip": "${BRIDGE_IP}",
            "dhcp": {
                "start": "${START}",
                "end": "${END}",
                "lease_time": 86400,
                "dns": ["8.8.8.8", "8.8.4.4"],
                "lease_file": "${RUNDIR}/qcontroller-dhcp-leases"
            },
            "dns": {
                "zone": ".",
                "resolv_conf": "/etc/resolv.conf"
            }
        }
    }
}
EOF
elif [[ "${MACOS_MODE}" == "MODE_BRIDGED" ]]; then
cat >${CONFIGDIR}/qemu-config.json <<EOF
{
    "port": ${QEMU_PORT},
    "macosSettings": {
        "mode": "${MACOS_MODE}",
        "bridged": {
            "interface": "${INTERFACE_NAME}"
        }
    }
}
EOF
else
# Extract IP addresses without CIDR suffix for macOS shared mode
START_IP=$(echo "${START}" | cut -d'/' -f1)
END_IP=$(echo "${END}" | cut -d'/' -f1)

# Calculate subnet from START CIDR (e.g., 192.168.71.4/24 -> 192.168.71.0/24)
IFS='/' read -r IP CIDR_BITS <<< "${START}"
IFS='.' read -r i1 i2 i3 i4 <<< "${IP}"
MASK=$((0xFFFFFFFF << (32 - CIDR_BITS) & 0xFFFFFFFF))
NETWORK_IP=$(printf "%d.%d.%d.%d" \
    $(((i1 & (MASK >> 24)) & 0xFF)) \
    $(((i2 & (MASK >> 16)) & 0xFF)) \
    $(((i3 & (MASK >> 8)) & 0xFF)) \
    $(((i4 & MASK) & 0xFF)))
SUBNET="${NETWORK_IP}/${CIDR_BITS}"

cat >${CONFIGDIR}/qemu-config.json <<EOF
{
    "port": ${QEMU_PORT},
    "macosSettings": {
        "mode": "${MACOS_MODE}",
        "shared": {
            "subnet": "${SUBNET}",
            "start_address": "${START_IP}",
            "end_address": "${END_IP}"
        }
    }
}
EOF
fi

QEMU_HOST="$(echo "${BRIDGE_IP}" | cut -d'/' -f1)"
if [[ "$(uname -s)" == "Darwin" ]]; then
    QEMU_HOST="localhost"
fi

cat >${CONFIGDIR}/controller-config.json <<EOF
{
    "port": ${CONTROLLER_PORT},
    "cache": {
        "root": "cache"
    },
    "root": "${ROOTDIR}",
    "qemuEndpoint": "${QEMU_HOST}:${QEMU_PORT}"
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
sudo LOG_LEVEL="${LOG_LEVEL}" ${QCONTROLLERD} qemu -c ${CONFIGDIR}/qemu-config.json >${LOGDIR}/qemu.out 2>${LOGDIR}/qemu.err &
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
