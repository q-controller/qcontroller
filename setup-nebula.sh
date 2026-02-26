#!/usr/bin/env bash

set -Eeuo pipefail

DIR=""
NODE_NAME=""
NODE_IP=""
NODE_ADDR=""
NODE_PORT=4242
PEER_NAME=""
PEER_IP=""
PEER_ADDR=""
PEER_PORT=4242
PEER_DIR=""
TUN_DEV=nebula1
NODE_UNSAFE_ROUTES=()
PEER_UNSAFE_ROUTES=()

usage() {
    cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") --dir DIR --name NAME --ip CIDR --addr HOST --peer-name NAME --peer-ip CIDR --peer-addr HOST [OPTIONS]

Generates nebula CA, certificates, and configs for two nodes.
Local node config is written to DIR, peer node config to DIR/peer.

Required:
  --dir                Output directory
  --name               Local node name
  --ip                 Local nebula IP in CIDR (e.g. 10.100.0.1/24)
  --addr               Local public address (e.g. 192.168.178.125)
  --peer-name          Peer node name
  --peer-ip            Peer nebula IP in CIDR (e.g. 10.100.0.2/24)
  --peer-addr          Peer public address (e.g. 192.168.178.200)
  --peer-dir           Path on the peer host where certs will be placed (e.g. /etc/nebula)

Optional:
  --port               Local listen port [default: ${NODE_PORT}]
  --peer-port          Peer listen port [default: ${PEER_PORT}]
  --tun-dev            Tun device name [default: ${TUN_DEV}]
  --unsafe-route       Local unsafe route (CIDR:VIA_NEBULA_IP, repeatable)
  --peer-unsafe-route  Peer unsafe route (CIDR:VIA_NEBULA_IP, repeatable)

Example:
  $(basename "${BASH_SOURCE[0]}") \\
    --dir /tmp/nebula \\
    --name host1 --ip 10.100.0.1/24 --addr 192.168.178.125 \\
    --peer-name host2 --peer-ip 10.100.0.2/24 --peer-addr 192.168.178.200 --peer-dir /etc/nebula \\
    --unsafe-route 192.168.26.0/24:10.100.0.2 \\
    --peer-unsafe-route 192.168.71.0/24:10.100.0.1
EOF
    exit
}

die() {
    echo "$1"
    exit "${2-1}"
}

while :; do
    case "${1-}" in
        -h | --help) usage ;;
        --dir) DIR="${2-}"; shift ;;
        --name) NODE_NAME="${2-}"; shift ;;
        --ip) NODE_IP="${2-}"; shift ;;
        --addr) NODE_ADDR="${2-}"; shift ;;
        --port) NODE_PORT="${2-}"; shift ;;
        --peer-name) PEER_NAME="${2-}"; shift ;;
        --peer-ip) PEER_IP="${2-}"; shift ;;
        --peer-addr) PEER_ADDR="${2-}"; shift ;;
        --peer-dir) PEER_DIR="${2-}"; shift ;;
        --peer-port) PEER_PORT="${2-}"; shift ;;
        --tun-dev) TUN_DEV="${2-}"; shift ;;
        --unsafe-route) NODE_UNSAFE_ROUTES+=("${2-}"); shift ;;
        --peer-unsafe-route) PEER_UNSAFE_ROUTES+=("${2-}"); shift ;;
        -?*) die "Unknown option: $1" ;;
        *) break ;;
    esac
    shift
done

[[ -z "${DIR}" ]] && die "Missing: --dir"
[[ -z "${NODE_NAME}" ]] && die "Missing: --name"
[[ -z "${NODE_IP}" ]] && die "Missing: --ip"
[[ -z "${NODE_ADDR}" ]] && die "Missing: --addr"
[[ -z "${PEER_NAME}" ]] && die "Missing: --peer-name"
[[ -z "${PEER_IP}" ]] && die "Missing: --peer-ip"
[[ -z "${PEER_ADDR}" ]] && die "Missing: --peer-addr"
[[ -z "${PEER_DIR}" ]] && die "Missing: --peer-dir"

# Extract IPs without CIDR for static_host_map
NODE_IP_BARE=$(echo "${NODE_IP}" | cut -d'/' -f1)
PEER_IP_BARE=$(echo "${PEER_IP}" | cut -d'/' -f1)

mkdir -p "${DIR}" "${DIR}/peer"

# Generate CA if not present
if [[ ! -f "${DIR}/ca.crt" || ! -f "${DIR}/ca.key" ]]; then
    echo "Generating CA..."
    nebula-cert ca -name "qcontroller" -out-crt "${DIR}/ca.crt" -out-key "${DIR}/ca.key"
else
    echo "Using existing CA"
fi

NODE_UNSAFE_NETWORKS=""
for route in "${PEER_UNSAFE_ROUTES[@]+"${PEER_UNSAFE_ROUTES[@]}"}"; do
    IFS=':' read -r cidr _ <<< "${route}"
    NODE_UNSAFE_NETWORKS="${NODE_UNSAFE_NETWORKS:+${NODE_UNSAFE_NETWORKS},}${cidr}"
done
PEER_UNSAFE_NETWORKS=""
for route in "${NODE_UNSAFE_ROUTES[@]+"${NODE_UNSAFE_ROUTES[@]}"}"; do
    IFS=':' read -r cidr _ <<< "${route}"
    PEER_UNSAFE_NETWORKS="${PEER_UNSAFE_NETWORKS:+${PEER_UNSAFE_NETWORKS},}${cidr}"
done

# Generate node cert
rm -f "${DIR}/${NODE_NAME}.crt" "${DIR}/${NODE_NAME}.key"
NODE_SIGN_ARGS=(-ca-crt "${DIR}/ca.crt" -ca-key "${DIR}/ca.key" \
    -name "${NODE_NAME}" -ip "${NODE_IP}" \
    -out-crt "${DIR}/${NODE_NAME}.crt" -out-key "${DIR}/${NODE_NAME}.key")
[[ -n "${NODE_UNSAFE_NETWORKS}" ]] && NODE_SIGN_ARGS+=(-unsafe-networks "${NODE_UNSAFE_NETWORKS}")
nebula-cert sign "${NODE_SIGN_ARGS[@]}"

# Generate peer cert
rm -f "${DIR}/peer/${PEER_NAME}.crt" "${DIR}/peer/${PEER_NAME}.key"
PEER_SIGN_ARGS=(-ca-crt "${DIR}/ca.crt" -ca-key "${DIR}/ca.key" \
    -name "${PEER_NAME}" -ip "${PEER_IP}" \
    -out-crt "${DIR}/peer/${PEER_NAME}.crt" -out-key "${DIR}/peer/${PEER_NAME}.key")
[[ -n "${PEER_UNSAFE_NETWORKS}" ]] && PEER_SIGN_ARGS+=(-unsafe-networks "${PEER_UNSAFE_NETWORKS}")
nebula-cert sign "${PEER_SIGN_ARGS[@]}"

# Copy CA to peer dir
cp "${DIR}/ca.crt" "${DIR}/peer/ca.crt"

build_routes() {
    shift  # skip "array name" — routes are passed as remaining args
    if [[ $# -gt 0 ]]; then
        echo "  unsafe_routes:"
        for route in "$@"; do
            IFS=':' read -r cidr via <<< "${route}"
            echo "    - route: ${cidr}"
            echo "      via: ${via}"
        done
    fi
}

generate_config() {
    local out_file=$1
    local cert_dir=$2
    local name=$3
    local listen_port=$4
    local remote_ip=$5
    local remote_addr=$6
    local remote_port=$7
    local routes_yaml=$8

    cat > "${out_file}" <<EOF
pki:
  ca: ${cert_dir}/ca.crt
  cert: ${cert_dir}/${name}.crt
  key: ${cert_dir}/${name}.key

static_host_map:
  "${remote_ip}": ["${remote_addr}:${remote_port}"]

lighthouse:
  am_lighthouse: false

cipher: chachapoly

listen:
  host: 0.0.0.0
  port: ${listen_port}

punchy:
  punch: true
  respond: true

tun:
  disabled: false
  mtu: 1400
  dev: ${TUN_DEV}
${routes_yaml}

logging:
  level: info
  format: text

firewall:
  default_local_cidr_any: true
  outbound:
    - port: any
      proto: any
      host: any
  inbound:
    - port: any
      proto: any
      host: any
EOF
}

NODE_ROUTES=$(build_routes _ "${NODE_UNSAFE_ROUTES[@]+"${NODE_UNSAFE_ROUTES[@]}"}")
PEER_ROUTES=$(build_routes _ "${PEER_UNSAFE_ROUTES[@]+"${PEER_UNSAFE_ROUTES[@]}"}")

# Local node config
generate_config "${DIR}/config.yml" "${DIR}" "${NODE_NAME}" "${NODE_PORT}" \
    "${PEER_IP_BARE}" "${PEER_ADDR}" "${PEER_PORT}" "${NODE_ROUTES}"

# Peer node config (paths reference where certs will live on the peer host)
generate_config "${DIR}/peer/config.yml" "${PEER_DIR}" "${PEER_NAME}" "${PEER_PORT}" \
    "${NODE_IP_BARE}" "${NODE_ADDR}" "${NODE_PORT}" "${PEER_ROUTES}"

echo "Local node: ${DIR}/config.yml"
echo "Peer node:  ${DIR}/peer/"
echo ""
echo "Run locally: sudo nebula -config ${DIR}/config.yml"
echo "Copy to peer: scp -r ${DIR}/peer/* <username>@${PEER_ADDR}:${PEER_DIR}/"
echo "Run on peer:  sudo nebula -config ${PEER_DIR}/config.yml"
