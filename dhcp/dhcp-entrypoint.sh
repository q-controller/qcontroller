#!/usr/bin/env bash

set -Eeuo pipefail
trap cleanup SIGINT SIGTERM ERR EXIT

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)
INTERFACE_NAME="krjakbrjakbr0"
LOCATION=$(mktemp -d)

usage() {
  cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-h] [-v] -n INTERFACE_NAME

Starts a dnsmasq server (DHCP only).

Available options:

-h, --help      Print this help and exit
-v, --verbose   Print script debug info
-n, --name      Interface name [default: ${INTERFACE_NAME}]
EOF
  exit
}

cleanup() {
  trap - SIGINT SIGTERM ERR EXIT
  # script cleanup here
  rm -fr ${LOCATION}
}

die() {
  local msg=$1
  local code=${2-1} # default exit status 1
  echo >&2 -e "$msg"
  exit "$code"
}

parse_params() {
  while :; do
    case "${1-}" in
    -h | --help) usage ;;
    -v | --verbose) set -x ;;
    -n | --name)
      INTERFACE_NAME="${2-}"
      shift
      ;;
    -?*) die "Unknown option: $1" ;;
    *) break ;;
    esac
    shift
  done

  args=("$@")

  return 0
}

parse_params "$@"

until ip link show ${INTERFACE_NAME} 2>/dev/null; do
  echo "Waiting for ${INTERFACE_NAME} to be CREATED..."
  sleep 1
done

while true; do
  IP_INFO=$(ip -o -f inet addr show "${INTERFACE_NAME}" | awk '{print $4}')
  if [ -n "${IP_INFO}" ]; then
    IFS='./' read -r a b c d mask <<< "${IP_INFO}"
    cat <<EOF > ${LOCATION}/dnsmasq.conf
port=0 # Disable DNS server
interface=${INTERFACE_NAME}
bind-interfaces
listen-address=${a}.${b}.${c}.${d}
dhcp-range=${a}.${b}.${c}.$((d+1)),${a}.${b}.${c}.$((d+100)),255.255.255.0,12h
dhcp-option=3,${a}.${b}.${c}.${d}  # Gateway
dhcp-option=6,8.8.8.8,1.1.1.1  # DNS
log-queries
log-dhcp
EOF
    break
  fi
done

until ip link show ${INTERFACE_NAME} | grep -q 'state UP'; do
  echo "Waiting for ${INTERFACE_NAME} to be UP..."
  sleep 1
done

exec dnsmasq -k --conf-file=${LOCATION}/dnsmasq.conf --dhcp-ignore-clid --dhcp-authoritative --dhcp-no-override
