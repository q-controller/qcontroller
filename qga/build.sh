#!/usr/bin/env bash

set -Eeuo pipefail
trap cleanup SIGINT SIGTERM ERR EXIT

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)

cleanup() {
    trap - SIGINT SIGTERM ERR EXIT
}

usage() {
	cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-h] [-v] --out-dir PATH --qemu-dir PATH

Builds a QEMU guest agent

Available options:

-h, --help            Print this help and exit
-v, --verbose         Print script debug info
--out-dir             Path to an output folder
--qemu-dir            Path to QEMU folder
EOF
	exit
}

OUTDIR=""
QEMUDIR=""
parse_params() {
	while :; do
		case "${1-}" in
		-h | --help) usage ;;
		-v | --verbose) set -x ;;
		--out-dir)
        OUTDIR="${2-}"
        if [[ ! "${OUTDIR}" == /* ]]; then
            OUTDIR="$(pwd)/${OUTDIR}"
        fi
        shift
        ;;
		--qemu-dir)
        QEMUDIR="${2-}"
        if [[ ! "${QEMUDIR}" == /* ]]; then
            QEMUDIR="$(pwd)/${QEMUDIR}"
        fi
        shift
        ;;
		-?*) echo "Unknown option: $1" && exit 1 ;;
		*) break ;;
		esac
		shift
	done

	args=("$@")
    [ -z "${OUTDIR}" ] && echo "Missing parameter: --out-dir" && exit 1
    [ -z "${QEMUDIR}" ] && echo "Missing parameter: --qemu-dir" && exit 1
    [ ! -d "${QEMUDIR}" ] && echo "QEMU dir does not exist" && exit 1

	return 0
}

parse_params "$@"

mkdir -p ${OUTDIR}

pushd ${script_dir}
docker build -t qgabuild .
docker run --rm -i -u $(id -u):$(id -g) -v ${QEMUDIR}:/qemu -v ${OUTDIR}:/out qgabuild bash <<EOF
cd /tmp && mkdir -p build && cd build
/qemu/configure --enable-guest-agent --disable-tools --disable-docs --disable-system --disable-user --disable-linux-user --disable-vnc --disable-werror --prefix=/out
make qemu-ga -j8
mkdir -p /out/bin
install -m755 qga/qemu-ga /out/bin/qemu-ga
mkdir -p /out/usr/lib/systemd/system
install /qemu/contrib/systemd/qemu-guest-agent.service /out/usr/lib/systemd/system/qemu-guest-agent.service
EOF
popd
