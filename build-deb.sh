#!/usr/bin/env bash
set -Eeuo pipefail
trap cleanup SIGINT SIGTERM ERR EXIT

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)
TEMP_DIR="$(mktemp -d)"

OUT_DIR="${script_dir}/build"
PKG_NAME="qcontrollerd"
PKG_VERSION="0.0.1"
ARCH=""

QEMU_PORT=9001
EVENTSERVICE_PORT=9002
FILEREGISTRY_PORT=9003
CONTROLLER_PORT=9004
ORCHESTRATOR_PORT=8080

# Linux: the qemu service binds inside its own network namespace. From the
# host (controller), reach it via the bridge IP; from the namespace (qemu),
# reach the host's fileregistry via the gateway IP.
GATEWAY_IP=192.168.71.1
BRIDGE_IP=192.168.71.3

SERVICES=(eventservice fileregistry qemu controller orchestrator)

die () {
    local msg=$1
    local code=${2-1}
    echo "$msg" >&2
    exit "$code"
}

cleanup() {
    local exit_code=$?
    trap - SIGINT SIGTERM ERR EXIT
    rm -rf "$TEMP_DIR"
    exit "$exit_code"
}

usage() {
    cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-h] [-v] [--arch <amd64|arm64>] [--version <ver>] [-o <out>]

Package an already-built qcontrollerd binary into a Debian package.
The binary is expected at build/qcontrollerd; produce it with 'make'
(or 'make deb' which does both).

Options:
  -h, --help          Print this help and exit
  -v, --verbose       Print script debug info
  --arch              Target architecture (amd64|arm64) [default: \$GOARCH or host]
  --version           Package version [default: ${PKG_VERSION}]
  -o, --out           Output directory [default: ${OUT_DIR}]
EOF
    exit
}

parse_params() {
    while :; do
        case "${1-}" in
            -h | --help) usage ;;
            -v | --verbose) set -x ;;
            --arch)
                ARCH="${2-}"
                shift
                ;;
            --version)
                PKG_VERSION="${2-}"
                shift
                ;;
            -o | --out)
                OUT_DIR="${2-}"
                shift
                ;;
            -?*) die "Unknown option: $1" ;;
            *) break ;;
        esac
        shift
    done

    if [[ -z "${ARCH-}" ]]; then
        if [[ -n "${GOARCH-}" ]]; then
            ARCH="${GOARCH}"
        elif command -v dpkg >/dev/null 2>&1; then
            ARCH="$(dpkg --print-architecture)"
        else
            case "$(uname -m)" in
                x86_64) ARCH="amd64" ;;
                aarch64) ARCH="arm64" ;;
                *) die "Cannot detect host architecture; pass --arch explicitly." ;;
            esac
        fi
    fi

    case "$ARCH" in
        amd64|arm64) ;;
        *) die "Unsupported --arch '$ARCH' (supported: amd64, arm64)" ;;
    esac

    mkdir -p "${OUT_DIR}"
}

write_configs() {
    local etc="${PKGROOT}/etc/qcontrollerd"
    mkdir -p \
        "${etc}/eventservice" \
        "${etc}/fileregistry" \
        "${etc}/qemu" \
        "${etc}/controller" \
        "${etc}/orchestrator"

    cat > "${etc}/eventservice/config.json" <<EOF
{
    "port": ${EVENTSERVICE_PORT}
}
EOF

    cat > "${etc}/fileregistry/config.json" <<EOF
{
    "port":           ${FILEREGISTRY_PORT},
    "root":           "/var/lib/qcontrollerd/fileregistry",
    "cache":          { "root": "cache" },
    "eventsEndpoint": "localhost:${EVENTSERVICE_PORT}"
}
EOF

    cat > "${etc}/qemu/config.json" <<EOF
{
    "port": ${QEMU_PORT},
    "root": "/var/lib/qcontrollerd/qemu",
    "linuxSettings": {
        "network": {
            "name":      "qcbr0",
            "gatewayIp": "${GATEWAY_IP}/24",
            "bridgeIp":  "${BRIDGE_IP}/24",
            "dhcp": {
                "start":     "192.168.71.10/24",
                "end":       "192.168.71.254/24",
                "leaseTime": 86400,
                "leaseFile": "/var/lib/qcontrollerd/qemu/dhcp.leases"
            },
            "dns": {
                "zone":       ".",
                "resolvConf": "/etc/resolv.conf"
            }
        }
    },
    "fileRegistryEndpoint": "${GATEWAY_IP}:${FILEREGISTRY_PORT}",
    "allowEmulationFallback": false
}
EOF

    cat > "${etc}/controller/config.json" <<EOF
{
    "port": ${CONTROLLER_PORT},
    "root": "/var/lib/qcontrollerd/controller",
    "local": {
        "name":                 "local",
        "endpoint":             "${BRIDGE_IP}:${QEMU_PORT}",
        "fileRegistryEndpoint": "localhost:${FILEREGISTRY_PORT}",
        "eventsEndpoint":       "localhost:${EVENTSERVICE_PORT}"
    },
    "eventsEndpoint": "localhost:${EVENTSERVICE_PORT}"
}
EOF

    cat > "${etc}/orchestrator/config.json" <<EOF
{
    "port": ${ORCHESTRATOR_PORT},
    "nodes": [
        {
            "name":                 "local",
            "endpoint":             "localhost:${CONTROLLER_PORT}",
            "fileRegistryEndpoint": "localhost:${FILEREGISTRY_PORT}",
            "eventsEndpoint":       "localhost:${EVENTSERVICE_PORT}"
        }
    ],
    "fileRegistryEndpoint": "localhost:${FILEREGISTRY_PORT}",
    "exposeSwaggerUi":      false
}
EOF
}

# Per-service systemd dependency DAG. Each service lists the qcontrollerd
# services it actually relies on; systemd computes the boot order.
service_deps() {
    case "$1" in
        eventservice) echo "" ;;
        fileregistry) echo "qcontrollerd-eventservice.service" ;;
        qemu)         echo "qcontrollerd-fileregistry.service" ;;
        controller)   echo "qcontrollerd-qemu.service qcontrollerd-eventservice.service" ;;
        orchestrator) echo "qcontrollerd-controller.service qcontrollerd-fileregistry.service qcontrollerd-eventservice.service" ;;
        *) die "unknown service: $1" ;;
    esac
}

write_units() {
    local units="${PKGROOT}/lib/systemd/system"
    mkdir -p "${units}"

    for svc in "${SERVICES[@]}"; do
        local unit="${units}/qcontrollerd-${svc}.service"
        local deps
        deps="$(service_deps "$svc")"
        local after="network-online.target"
        local wants="network-online.target"
        if [[ -n "$deps" ]]; then
            after="${deps} ${after}"
            wants="${deps} ${wants}"
        fi

        cat > "${unit}" <<EOF
[Unit]
Description=qcontrollerd ${svc}
After=${after}
Wants=${wants}

[Service]
ExecStart=/usr/bin/qcontrollerd ${svc} -c /etc/qcontrollerd/${svc}/config.json
Restart=on-failure
RestartSec=2
EOF

        # qemu spawns qemu-system-* subprocesses (one per VM) that must
        # survive a service restart. KillMode=process tells systemd to
        # signal only the main process; the children (already detached via
        # Setsid) get reparented to PID 1 and qcontrollerd reattaches to
        # them by pidfile on next start.
        if [[ "$svc" == "qemu" ]]; then
            cat >> "${unit}" <<EOF
KillMode=process
EOF
        else
            cat >> "${unit}" <<EOF
User=qcontroller
Group=qcontroller
EOF
        fi

        cat >> "${unit}" <<EOF

[Install]
WantedBy=multi-user.target
EOF
    done
}

write_binary() {
    local bin_dir="${PKGROOT}/usr/bin"
    mkdir -p "${bin_dir}"
    install -m 0755 "${script_dir}/build/qcontrollerd" "${bin_dir}/qcontrollerd"
}

# Format the project LICENSE as a Debian-policy copyright file
# (https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/).
write_copyright() {
    local doc_dir="${PKGROOT}/usr/share/doc/qcontrollerd"
    mkdir -p "${doc_dir}"
    [[ -f "${script_dir}/LICENSE" ]] || die "LICENSE file not found at ${script_dir}/LICENSE"

    {
        cat <<EOF
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: qcontrollerd
Source: https://github.com/q-controller/qcontroller

Files: *
Copyright: 2025 Nikita Vakula
License: MIT

License: MIT
EOF
        # Indent LICENSE per Debian copyright format: every line prefixed with
        # a single space; empty lines become ' .'.
        sed -e 's/^/ /' -e 's/^ $/ ./' "${script_dir}/LICENSE"
    } > "${doc_dir}/copyright"
}

# qemu-system-* picks the right binaries for the target arch.
qemu_system_dep() {
    case "$1" in
        amd64) echo "qemu-system-x86" ;;
        arm64) echo "qemu-system-arm" ;;
        *) die "no qemu-system mapping for arch $1" ;;
    esac
}

write_control() {
    local debian="${PKGROOT}/DEBIAN"
    mkdir -p "${debian}"

    local qemu_sys
    qemu_sys="$(qemu_system_dep "${ARCH}")"

    cat > "${debian}/control" <<EOF
Package: ${PKG_NAME}
Version: ${PKG_VERSION}
Architecture: ${ARCH}
Maintainer: Nikita Vakula <programmistov.programmist@gmail.com>
Section: admin
Priority: optional
Depends: systemd, adduser, qemu-utils, ${qemu_sys}, genisoimage
Description: API-driven tool for managing QEMU-based virtual machine instances
 qcontroller is a flexible, API-driven tool for managing QEMU-based
 virtual machine instances. Each node runs qemu, fileregistry,
 eventservice and controller services on localhost; the orchestrator
 sits on top, exposing a REST API, WebSocket event stream and web UI.
EOF

    cat > "${debian}/conffiles" <<EOF
/etc/qcontrollerd/eventservice/config.json
/etc/qcontrollerd/fileregistry/config.json
/etc/qcontrollerd/qemu/config.json
/etc/qcontrollerd/controller/config.json
/etc/qcontrollerd/orchestrator/config.json
EOF
}

write_maintainer_scripts() {
    local debian="${PKGROOT}/DEBIAN"

    cat > "${debian}/preinst" <<'EOF'
#!/usr/bin/env bash
set -e

SERVICES=(eventservice fileregistry qemu controller orchestrator)

case "$1" in
    install|upgrade)
        for ((i=${#SERVICES[@]}-1; i>=0; i--)); do
            unit="qcontrollerd-${SERVICES[$i]}.service"
            if systemctl is-active --quiet "$unit" 2>/dev/null; then
                systemctl stop "$unit" || true
            fi
        done
        ;;
esac

exit 0
EOF
    chmod 755 "${debian}/preinst"

    cat > "${debian}/postinst" <<EOF
#!/usr/bin/env bash
set -e

SERVICES=(eventservice fileregistry qemu controller orchestrator)
QEMU_PORT=${QEMU_PORT}
EVENTSERVICE_PORT=${EVENTSERVICE_PORT}
FILEREGISTRY_PORT=${FILEREGISTRY_PORT}
CONTROLLER_PORT=${CONTROLLER_PORT}
ORCHESTRATOR_PORT=${ORCHESTRATOR_PORT}

case "\$1" in
    configure)
        if ! getent group qcontroller >/dev/null; then
            addgroup --system qcontroller
        fi
        if ! getent passwd qcontroller >/dev/null; then
            adduser --system --no-create-home --shell /usr/sbin/nologin \\
                --ingroup qcontroller qcontroller
        fi

        # qemu service runs as root; other services as qcontroller.
        for svc in "\${SERVICES[@]}"; do
            install -d -m 0750 "/var/lib/qcontrollerd/\${svc}"
            if [[ "\$svc" == "qemu" ]]; then
                chown root:root "/var/lib/qcontrollerd/\${svc}"
                chown root:root "/etc/qcontrollerd/\${svc}"
                chmod 0750 "/etc/qcontrollerd/\${svc}"
                chown root:root "/etc/qcontrollerd/\${svc}/config.json"
                chmod 0640 "/etc/qcontrollerd/\${svc}/config.json"
            else
                chown qcontroller:qcontroller "/var/lib/qcontrollerd/\${svc}"
                chown qcontroller:qcontroller "/etc/qcontrollerd/\${svc}"
                chmod 0750 "/etc/qcontrollerd/\${svc}"
                chown qcontroller:qcontroller "/etc/qcontrollerd/\${svc}/config.json"
                chmod 0640 "/etc/qcontrollerd/\${svc}/config.json"
            fi
        done

        systemctl daemon-reload

        for svc in "\${SERVICES[@]}"; do
            systemctl enable --now "qcontrollerd-\${svc}.service" || true
        done

        cat <<MSG
============================================
qcontrollerd installation complete.
Services:
  qemu          → localhost:\${QEMU_PORT}
  eventservice  → localhost:\${EVENTSERVICE_PORT}
  fileregistry  → localhost:\${FILEREGISTRY_PORT}
  controller    → localhost:\${CONTROLLER_PORT}
  orchestrator  → http://localhost:\${ORCHESTRATOR_PORT}/ui/

Configs:  /etc/qcontrollerd/<service>/config.json
Data:     /var/lib/qcontrollerd/<service>/
Logs:     journalctl -u qcontrollerd-<service>

Swagger UI is disabled by default. Enable it by setting
'exposeSwaggerUi' to true in /etc/qcontrollerd/orchestrator/config.json
and restarting qcontrollerd-orchestrator.

If the qemu service can't find qemu/qemu-img/genisoimage on its PATH,
pin absolute paths in /etc/qcontrollerd/qemu/config.json under a
'binaries' block, then:
  sudo systemctl restart qcontrollerd-qemu

To remove: sudo apt remove qcontrollerd  (keeps configs)
To purge:  sudo apt purge qcontrollerd   (removes everything)
============================================
MSG
        ;;
esac

exit 0
EOF
    chmod 755 "${debian}/postinst"

    cat > "${debian}/prerm" <<'EOF'
#!/usr/bin/env bash
set -e

SERVICES=(eventservice fileregistry qemu controller orchestrator)

case "$1" in
    remove|upgrade|deconfigure)
        for ((i=${#SERVICES[@]}-1; i>=0; i--)); do
            unit="qcontrollerd-${SERVICES[$i]}.service"
            if systemctl is-active --quiet "$unit" 2>/dev/null; then
                systemctl stop "$unit" || true
            fi
            if [[ "$1" == "remove" ]]; then
                systemctl disable "$unit" 2>/dev/null || true
            fi
        done
        ;;
esac

exit 0
EOF
    chmod 755 "${debian}/prerm"

    cat > "${debian}/postrm" <<'EOF'
#!/usr/bin/env bash
set -e

# Kill any running qemu-system-* processes spawned by qcontroller. The qemu
# systemd unit uses KillMode=process so VMs survive a service restart; this
# is desirable on `upgrade` (the new qcontrollerd reattaches via pidfiles)
# but on `remove` and `purge` the binary is gone, leaving orphan VMs the
# user can no longer manage.
kill_running_vms() {
    [[ -d /var/lib/qcontrollerd/qemu/instances ]] || return 0
    local pids=() pid pidfile alive
    for pidfile in /var/lib/qcontrollerd/qemu/instances/*/pid; do
        [[ -f "$pidfile" ]] || continue
        pid=$(cat "$pidfile" 2>/dev/null) || continue
        [[ -n "$pid" ]] || continue
        if kill -0 "$pid" 2>/dev/null; then
            kill -TERM "$pid" 2>/dev/null || true
            pids+=("$pid")
        fi
    done
    (( ${#pids[@]} > 0 )) || return 0
    # Wait up to ~5s for graceful exit, then SIGKILL holdouts.
    for _ in 1 2 3 4 5; do
        sleep 1
        alive=0
        for pid in "${pids[@]}"; do
            kill -0 "$pid" 2>/dev/null && alive=1
        done
        (( alive == 0 )) && break
    done
    for pid in "${pids[@]}"; do
        kill -0 "$pid" 2>/dev/null && kill -KILL "$pid" 2>/dev/null || true
    done
}

case "$1" in
    remove)
        systemctl daemon-reload || true
        kill_running_vms
        ;;
    purge)
        systemctl daemon-reload || true
        kill_running_vms
        rm -rf /var/lib/qcontrollerd
        if getent passwd qcontroller >/dev/null; then
            deluser --quiet --system qcontroller >/dev/null 2>&1 || true
        fi
        if getent group qcontroller >/dev/null; then
            delgroup --quiet --system --only-if-empty qcontroller >/dev/null 2>&1 || true
        fi
        ;;
esac

exit 0
EOF
    chmod 755 "${debian}/postrm"
}

build_deb() {
    local out="${OUT_DIR}/${PKG_NAME}_${PKG_VERSION}_${ARCH}.deb"
    dpkg-deb --build --root-owner-group "${PKGROOT}" "${out}"
    echo "Package built at: ${out}"
}

parse_params "$@"

command -v dpkg-deb >/dev/null || die "dpkg-deb not found; install dpkg-dev."

PKGROOT="${TEMP_DIR}/pkgroot"
mkdir -p "${PKGROOT}"

[[ -x "${script_dir}/build/qcontrollerd" ]] \
    || die "build/qcontrollerd not found. Run 'make' first, or use 'make deb'."

write_binary
write_configs
write_units
write_copyright
write_control
write_maintainer_scripts
build_deb
