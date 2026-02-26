#!/usr/bin/env bash

set -Eeuo pipefail

BRIDGE_CIDR=""
OVERLAY=""
ACTION=""

usage() {
    cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") {add|remove} --bridge-cidr CIDR --overlay INTERFACE

Adds or removes nft forwarding rules between the qemu bridge and an overlay interface.
The bridge interface is discovered by its address.

Examples:
  $(basename "${BASH_SOURCE[0]}") add --bridge-cidr 192.168.71.3/24 --overlay nebula1
  $(basename "${BASH_SOURCE[0]}") remove --bridge-cidr 192.168.71.3/24 --overlay nebula1
EOF
    exit
}

die() {
    echo "$1"
    exit "${2-1}"
}

ACTION="${1-}"
[[ "$ACTION" == "add" || "$ACTION" == "remove" ]] || usage
shift

while :; do
    case "${1-}" in
        -h | --help) usage ;;
        --bridge-cidr)
            BRIDGE_CIDR="${2-}"
            shift
            ;;
        --overlay)
            OVERLAY="${2-}"
            shift
            ;;
        -?*) die "Unknown option: $1" ;;
        *) break ;;
    esac
    shift
done

[[ -z "${BRIDGE_CIDR}" ]] && die "Missing required parameter: --bridge-cidr"
[[ -z "${OVERLAY}" ]] && die "Missing required parameter: --overlay"

# Discover bridge interface by its address
BRIDGE=$(ip -o addr show to "${BRIDGE_CIDR}" | awk '{print $2; exit}')
[[ -z "${BRIDGE}" ]] && die "No interface found with address ${BRIDGE_CIDR}"

echo "${ACTION}: ${BRIDGE} <-> ${OVERLAY}"

if [[ "$ACTION" == "add" ]]; then
    nft add rule ip filter FORWARD iifname "${BRIDGE}" oifname "${OVERLAY}" accept
    nft add rule ip filter FORWARD iifname "${OVERLAY}" oifname "${BRIDGE}" accept
else
    HANDLES=$(nft -a list chain ip filter FORWARD 2>/dev/null | awk -v b="${BRIDGE}" -v o="${OVERLAY}" '
        ($0 ~ "iifname.*\"" b "\"" && $0 ~ "oifname.*\"" o "\"") ||
        ($0 ~ "iifname.*\"" o "\"" && $0 ~ "oifname.*\"" b "\"") {
            print $NF
        }
    ')
    for handle in $HANDLES; do
        nft delete rule ip filter FORWARD handle "$handle"
    done
fi

echo "Done."
