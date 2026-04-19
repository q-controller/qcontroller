#!/usr/bin/env bash

set -Eeuo pipefail

trap cleanup SIGINT SIGTERM ERR EXIT

export LOG_LEVEL=info

INTERFACE_NAME=br0
HOST_IP=192.168.71.1/24
BRIDGE_IP=""  # Will be derived from HOST_IP if not set
START=192.168.71.4/24
END=192.168.71.254/24
MACOS_MODE=MODE_SHARED
CONTROLLER_PORT=8009
QEMU_PORT=8008
ORCHESTRATOR_HTTP_PORT=8080
EVENTSERVICE_PORT=8011
FILEREGISTRY_PORT=8010
REGISTRY_ADDRESS=""
QCONTROLLERD=""
RUNDIR=""
USE_CERTS="false"
CERTDIR=""
pids=()

OS_TYPE="$(uname -s)"

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)

usage() {
    cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-h] --rundir PATH --bin PATH [--interface NAME] [--cidr VALUE] [--start IP] [--end IP]

Starts all qcontrollerd services (fileregistry, qemu, gateway, controller)

Available options:

-h, --help         Print this help and exit
--bin              Path to qcontrollerd binary
--rundir           Path to the rundir
--interface        Interface name [default: ${INTERFACE_NAME}] (Linux: bridge name, macOS: not used in shared mode)
--cidr             Gateway CIDR [default: ${HOST_IP}] (Linux only)
--start            Start of DHCP/IP range in CIDR notation [default: ${START}] (both platforms)
--end              End of DHCP/IP range in CIDR notation [default: ${END}] (both platforms)
--macos-mode       macOS network mode: MODE_BRIDGED or MODE_SHARED [default: ${MACOS_MODE}]
--certs            Generate a local CA and per-service certs, enabling mTLS for all gRPC
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
                    HOST_IP="${2-}"
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
            --certs)
                USE_CERTS="true"
                ;;
            -?*) die "Unknown option: $1" ;;
            *) break ;;
        esac
        shift
    done

    args=("$@")

    [[ -z "${RUNDIR-}" ]] && die "Missing required parameter: rundir"
    [[ -z "${QCONTROLLERD-}" ]] && die "Missing required parameter: bin"

    # Calculate registry address from local settings
    if [[ "$OS_TYPE" == "Linux" ]]; then
        HOST_IP_ADDR=$(echo "${HOST_IP}" | cut -d'/' -f1)
        REGISTRY_ADDRESS="${HOST_IP_ADDR}:${FILEREGISTRY_PORT}"
    else
        REGISTRY_ADDRESS="localhost:${FILEREGISTRY_PORT}"
    fi

    # Derive BRIDGE_IP from HOST_IP if not set (Linux only)
    if [[ "$OS_TYPE" == "Linux" ]] && [[ -z "${BRIDGE_IP}" ]]; then
        # Extract IP and CIDR from HOST_IP (e.g., 192.168.71.1/24)
        IFS='/' read -r HOST_IP_ADDR HOST_CIDR <<< "${HOST_IP}"
        IFS='.' read -r h1 h2 h3 h4 <<< "${HOST_IP_ADDR}"

        # Bridge IP = Host IP + 2 (e.g., 192.168.71.1 -> 192.168.71.3)
        BRIDGE_LAST_OCTET=$((h4 + 2))
        BRIDGE_IP="${h1}.${h2}.${h3}.${BRIDGE_LAST_OCTET}/${HOST_CIDR}"
    fi

    return 0
}

parse_params "$@"

LOGDIR=${RUNDIR}/logs
ROOTDIR=${RUNDIR}/root
CONFIGDIR=${RUNDIR}/configs
CERTDIR=${RUNDIR}/certs

mkdir -p ${CONFIGDIR}
mkdir -p ${ROOTDIR}
mkdir -p ${LOGDIR}

gen_cert() {
    local name=$1
    local sans=$2
    openssl genrsa -out "${CERTDIR}/${name}-key.pem" 2048 2>/dev/null
    openssl req -new -key "${CERTDIR}/${name}-key.pem" \
        -out "${CERTDIR}/${name}.csr" -subj "/CN=${name}" 2>/dev/null
    openssl x509 -req -in "${CERTDIR}/${name}.csr" \
        -CA "${CERTDIR}/ca.pem" -CAkey "${CERTDIR}/ca-key.pem" \
        -CAcreateserial -out "${CERTDIR}/${name}.pem" \
        -days 365 -sha256 \
        -extfile <(printf "subjectAltName=%s\nextendedKeyUsage=serverAuth,clientAuth" "${sans}") \
        2>/dev/null
    rm -f "${CERTDIR}/${name}.csr"
}

if [[ "${USE_CERTS}" == "true" ]]; then
    mkdir -p "${CERTDIR}"
    openssl genrsa -out "${CERTDIR}/ca-key.pem" 4096 2>/dev/null
    openssl req -new -x509 -days 365 -key "${CERTDIR}/ca-key.pem" \
        -out "${CERTDIR}/ca.pem" -subj "/CN=qcontroller-ca" 2>/dev/null

    # SANs match the address each server is actually dialed at.
    if [[ "$OS_TYPE" == "Linux" ]]; then
        HOST_IP_ONLY=$(echo "${HOST_IP}" | cut -d'/' -f1)
        BRIDGE_IP_ONLY=$(echo "${BRIDGE_IP}" | cut -d'/' -f1)
        QEMU_SANS="IP:${BRIDGE_IP_ONLY}"
        FILEREGISTRY_SANS="IP:${HOST_IP_ONLY}"
    else
        QEMU_SANS="DNS:localhost,IP:127.0.0.1"
        FILEREGISTRY_SANS="DNS:localhost,IP:127.0.0.1"
    fi
    LOCAL_SANS="DNS:localhost,IP:127.0.0.1"

    gen_cert qemu "${QEMU_SANS}"
    gen_cert fileregistry "${FILEREGISTRY_SANS}"
    gen_cert controller "${LOCAL_SANS}"
    gen_cert eventservice "${LOCAL_SANS}"
    gen_cert orchestrator "${LOCAL_SANS}"
fi

tls_block() {
    local field=$1
    local name=$2
    [[ "${USE_CERTS}" == "true" ]] || return 0
    echo ",\"${field}\": {\"ca\": \"${CERTDIR}/ca.pem\", \"cert\": \"${CERTDIR}/${name}.pem\", \"key\": \"${CERTDIR}/${name}-key.pem\"}"
}

if [[ "$OS_TYPE" == "Linux" ]]; then
cat >${CONFIGDIR}/qemu-config.json <<EOF
{
    "port": "${QEMU_PORT}",
    "root": "${ROOTDIR}/qemu",
    "fileRegistryEndpoint": "${REGISTRY_ADDRESS}",
    "linuxSettings": {
        "network": {
            "name": "${INTERFACE_NAME}",
            "gateway_ip": "${HOST_IP}",
            "bridge_ip": "${BRIDGE_IP}",
            "dhcp": {
                "start": "${START}",
                "end": "${END}",
                "lease_time": 86400,
                "lease_file": "${RUNDIR}/qcontroller-dhcp-leases"
            },
            "dns": {
                "zone": ".",
                "static": {
                    "endpoints": ["127.0.0.53:53"]
                }
            }
        }
    }$(tls_block tls qemu)$(tls_block fileRegistryTls qemu)
}
EOF
elif [[ "${MACOS_MODE}" == "MODE_BRIDGED" ]]; then
cat >${CONFIGDIR}/qemu-config.json <<EOF
{
    "port": ${QEMU_PORT},
    "root": "${ROOTDIR}/qemu",
    "fileRegistryEndpoint": "${REGISTRY_ADDRESS}",
    "macosSettings": {
        "mode": "${MACOS_MODE}",
        "bridged": {
            "interface": "${INTERFACE_NAME}"
        },
        "dns": {
            "zone": "."
        }
    }$(tls_block tls qemu)$(tls_block fileRegistryTls qemu)
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
    "root": "${ROOTDIR}/qemu",
    "fileRegistryEndpoint": "${REGISTRY_ADDRESS}",
    "macosSettings": {
        "mode": "${MACOS_MODE}",
        "shared": {
            "subnet": "${SUBNET}",
            "start_address": "${START_IP}",
            "end_address": "${END_IP}"
        },
        "dns": {
            "zone": "."
        }
    }$(tls_block tls qemu)$(tls_block fileRegistryTls qemu)
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
    "root": "${ROOTDIR}",
    "local": {"name": "local", "endpoint": "${QEMU_HOST}:${QEMU_PORT}"},
    "eventsEndpoint": "localhost:${EVENTSERVICE_PORT}"$(tls_block tls controller)$(tls_block qemuTls controller)$(tls_block eventsTls controller)
}
EOF

cat >${CONFIGDIR}/fileregistry-config.json <<EOF
{
    "port": ${FILEREGISTRY_PORT},
    "cache": {
        "root": "cache"
    },
    "root": "${ROOTDIR}",
    "eventsEndpoint": "localhost:${EVENTSERVICE_PORT}"$(tls_block tls fileregistry)$(tls_block eventsTls fileregistry)
}
EOF

cat >${CONFIGDIR}/orchestrator-config.json <<EOF
{
    "port": ${ORCHESTRATOR_HTTP_PORT},
    "nodes": [
        {"name": "local", "endpoint": "localhost:${CONTROLLER_PORT}", "fileRegistryEndpoint": "${REGISTRY_ADDRESS}", "eventsEndpoint": "localhost:${EVENTSERVICE_PORT}"$(tls_block controllerTls orchestrator)$(tls_block fileRegistryTls orchestrator)$(tls_block eventsTls orchestrator)}
    ],
    "fileRegistryEndpoint": "${REGISTRY_ADDRESS}",
    "exposeSwaggerUi": true$(tls_block fileRegistryTls orchestrator)$(tls_block tls orchestrator)
}
EOF

cat >${CONFIGDIR}/eventservice-config.json <<EOF
{
    "port": ${EVENTSERVICE_PORT}$(tls_block tls eventservice)
}
EOF

touch ${LOGDIR}/eventservice.out
touch ${LOGDIR}/eventservice.err
${QCONTROLLERD} eventservice -c ${CONFIGDIR}/eventservice-config.json >${LOGDIR}/eventservice.out 2>${LOGDIR}/eventservice.err &
pids+=($!)

# Wait until eventservice is ready
sleep 1

touch ${LOGDIR}/fileregistry.out
touch ${LOGDIR}/fileregistry.err
${QCONTROLLERD} fileregistry -c ${CONFIGDIR}/fileregistry-config.json >${LOGDIR}/fileregistry.out 2>${LOGDIR}/fileregistry.err &
pids+=($!)

# Wait until fileregistry is ready
sleep 1

touch ${LOGDIR}/qemu.out
touch ${LOGDIR}/qemu.err
sudo -v
sudo LOG_LEVEL="${LOG_LEVEL}" ${QCONTROLLERD} qemu -c ${CONFIGDIR}/qemu-config.json >${LOGDIR}/qemu.out 2>${LOGDIR}/qemu.err &
pids+=($!)

# Wait until qemu is ready
sleep 1

touch ${LOGDIR}/orchestrator.out
touch ${LOGDIR}/orchestrator.err
${QCONTROLLERD} orchestrator -c ${CONFIGDIR}/orchestrator-config.json >${LOGDIR}/orchestrator.out 2>${LOGDIR}/orchestrator.err &
pids+=($!)

# Wait until orchestrator (EventService) is ready
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
