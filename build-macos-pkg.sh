#!/usr/bin/env bash
set -Eeuo pipefail
trap cleanup SIGINT SIGTERM ERR EXIT

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd -P)
TEMP_DIR="$(mktemp -d)"
OUT_DIR="${script_dir}/build"
PKG_DIR="${OUT_DIR}/pkg"
ROOT_DIR="${PKG_DIR}/pkgroot"
SCRIPTS_DIR="${PKG_DIR}/scripts"
APPID="com.github.qcontroller.qcontrollerd"
PKG_VERSION="0.0.1"

# Default ports for the single-node install. Users can edit these in
# /Library/Application Support/<APPID>.<service>/config.json after install.
QEMU_PORT=9001
EVENTSERVICE_PORT=9002
FILEREGISTRY_PORT=9003
CONTROLLER_PORT=9004
ORCHESTRATOR_PORT=8080

# Service ordering matters at boot: producers before consumers.
# Bootstrap order: eventservice → fileregistry → qemu → controller → orchestrator
# Teardown order: reverse.
SERVICES=(eventservice fileregistry qemu controller orchestrator)

QEMU_CONFIG=$(cat <<EOF
{
    "port": ${QEMU_PORT},
    "root":  "/Library/Application Support/${APPID}.qemu/root",
    "macosSettings": {
        "mode": "MODE_SHARED",
        "shared": {
            "subnet":       "192.168.33.0/24",
            "startAddress": "192.168.33.1",
            "endAddress":   "192.168.33.254"
        },
        "dns": { "resolvConf": "/etc/resolv.conf" }
    },
    "fileRegistryEndpoint": "localhost:${FILEREGISTRY_PORT}"
}
EOF
)

EVENTSERVICE_CONFIG=$(cat <<EOF
{
    "port": ${EVENTSERVICE_PORT}
}
EOF
)

FILEREGISTRY_CONFIG=$(cat <<EOF
{
    "port":           ${FILEREGISTRY_PORT},
    "root":           "/Library/Application Support/${APPID}.fileregistry/data",
    "cache":          { "root": "cache" },
    "eventsEndpoint": "localhost:${EVENTSERVICE_PORT}"
}
EOF
)

CONTROLLER_CONFIG=$(cat <<EOF
{
    "port": ${CONTROLLER_PORT},
    "root": "/Library/Application Support/${APPID}.controller/data",
    "local": {
        "name":                 "local",
        "endpoint":             "localhost:${QEMU_PORT}",
        "fileRegistryEndpoint": "localhost:${FILEREGISTRY_PORT}",
        "eventsEndpoint":       "localhost:${EVENTSERVICE_PORT}"
    },
    "eventsEndpoint": "localhost:${EVENTSERVICE_PORT}"
}
EOF
)

ORCHESTRATOR_CONFIG=$(cat <<EOF
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
    "exposeSwaggerUi":      true
}
EOF
)

config_for() {
    case "$1" in
        qemu)         printf '%s' "$QEMU_CONFIG" ;;
        eventservice) printf '%s' "$EVENTSERVICE_CONFIG" ;;
        fileregistry) printf '%s' "$FILEREGISTRY_CONFIG" ;;
        controller)   printf '%s' "$CONTROLLER_CONFIG" ;;
        orchestrator) printf '%s' "$ORCHESTRATOR_CONFIG" ;;
        *) die "unknown service: $1" ;;
    esac
}

die () {
    local msg=$1
    local code=${2-1}
    echo "$msg"
    exit "$code"
}

cleanup() {
    local exit_code=$?
    trap - SIGINT SIGTERM ERR EXIT
    echo "Cleaning up..."
    rm -rf "$TEMP_DIR"
    exit "$exit_code"
}

usage() {
    cat <<EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-h] [-v] [-o out_dir]
Build the macOS installer package for qcontrollerd.
Available options:
  -h, --help      Print this help and exit
  -v, --verbose   Print script debug info
  -o, --out       Output directory for built artifacts [default: ${OUT_DIR}]
EOF
    exit
}

parse_params() {
    while :; do
        case "${1-}" in
            -h | --help) usage ;;
            -v | --verbose) set -x ;;
            -o | --out)
                OUT_DIR="${2-}"
                shift
                ;;
            -?*) die "Unknown option: $1" ;;
            *) break ;;
        esac
        shift
    done

    [[ -z "${OUT_DIR-}" ]] && die "Missing required parameter: out_dir"
    mkdir -p "${OUT_DIR}"
}

parse_params "$@"


LAUNCHD_PATH="/usr/bin:/bin:/usr/sbin:/sbin"

create_plist() {
    local SERVICE="$1"
    local PLIST_DIR="${ROOT_DIR}/Library/LaunchDaemons"
    local CONFIG_DIR="${ROOT_DIR}/Library/Application Support/${APPID}.${SERVICE}"
    local LOG_DIR="${ROOT_DIR}/var/log/${APPID}.${SERVICE}"

    mkdir -p "${PLIST_DIR}" "${CONFIG_DIR}" "${LOG_DIR}"

    cat > "${PLIST_DIR}/${APPID}.${SERVICE}.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${APPID}.${SERVICE}</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/qcontrollerd</string>
        <string>${SERVICE}</string>
        <string>-c</string>
        <string>/Library/Application Support/${APPID}.${SERVICE}/config.json</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${LAUNCHD_PATH}</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/${APPID}.${SERVICE}/${SERVICE}.out</string>
    <key>StandardErrorPath</key>
    <string>/var/log/${APPID}.${SERVICE}/${SERVICE}.err</string>
</dict>
</plist>
EOF
    chmod 644 "${PLIST_DIR}/${APPID}.${SERVICE}.plist"

    config_for "${SERVICE}" > "${CONFIG_DIR}/config.json"
    chmod 644 "${CONFIG_DIR}/config.json"
}

setup_binary() {
    "${script_dir}/build.sh" "${OUT_DIR}"
    local BIN_DIR="${ROOT_DIR}/usr/local/bin"
    mkdir -p "${BIN_DIR}"
    cp "${OUT_DIR}/qcontrollerd" "${BIN_DIR}/qcontrollerd"
    chmod 755 "${BIN_DIR}/qcontrollerd"
}

create_all_plists() {
    local svc
    for svc in "${SERVICES[@]}"; do
        create_plist "$svc"
    done
}

# uninstaller (shipped under /usr/local/share/<APPID>/)
setup_uninstall_script() {
    local UNINSTALLER_DIR="${ROOT_DIR}/usr/local/share/${APPID}"
    mkdir -p "${UNINSTALLER_DIR}"
    cat > "${UNINSTALLER_DIR}/uninstall.sh" <<EOF
#!/usr/bin/env bash
set -e

APPID="${APPID}"
SERVICES=(${SERVICES[*]})
HELPER_DIR="/usr/local/share/\${APPID}"

SCRIPT_PATH="\$(realpath "\$0")"
SCRIPT_DIR="\$(dirname "\$SCRIPT_PATH")"
if [[ "\$SCRIPT_DIR" != "\$HELPER_DIR" ]]; then
    echo "Running uninstall from a wrong location: \$SCRIPT_DIR"
    exit 1
fi

echo "Uninstalling qcontrollerd..."

unload_service() {
    local SERVICE="\$1"
    local LABEL="\${APPID}.\${SERVICE}"
    local PLIST="/Library/LaunchDaemons/\${LABEL}.plist"

    if launchctl print "system/\${LABEL}" >/dev/null 2>&1; then
        echo "Stopping system service: \$LABEL"
        sudo launchctl bootout system "\$PLIST" || true
    fi
    sudo pkill -f "qcontrollerd \$SERVICE" 2>/dev/null || true
    sudo rm -f "\$PLIST"
}

# Reverse boot order during teardown.
for ((i=\${#SERVICES[@]}-1; i>=0; i--)); do
    unload_service "\${SERVICES[\$i]}"
done

# Remove binary, config dirs, log dirs (system-wide install — no per-user state).
sudo rm -f /usr/local/bin/qcontrollerd
for svc in "\${SERVICES[@]}"; do
    sudo rm -rf "/Library/Application Support/\${APPID}.\${svc}"
    sudo rm -rf "/var/log/\${APPID}.\${svc}"
done

sudo pkgutil --forget "\${APPID}" 2>/dev/null || true

rm -f "\$0" 2>/dev/null || true
sudo rm -rf "\$HELPER_DIR"

echo "============================================"
echo "qcontrollerd has been completely uninstalled."
echo "============================================"
EOF
    chmod 755 "${UNINSTALLER_DIR}/uninstall.sh"
}

create_scripts() {
    mkdir -p "${SCRIPTS_DIR}"

    cat > "${SCRIPTS_DIR}/preinstall" <<EOF
#!/usr/bin/env bash
set -e

APPID="${APPID}"
SERVICES=(${SERVICES[*]})

unload_service() {
    local SERVICE="\$1"
    local LABEL="\${APPID}.\${SERVICE}"
    local PLIST="/Library/LaunchDaemons/\${LABEL}.plist"

    if launchctl print "system/\${LABEL}" >/dev/null 2>&1; then
        echo "Stopping existing system service: \$LABEL"
        launchctl bootout "system/\${LABEL}" "\${PLIST}" || true
    fi
    rm -f "\${PLIST}"
}

# Reverse boot order on teardown so consumers shut down before producers.
for ((i=\${#SERVICES[@]}-1; i>=0; i--)); do
    unload_service "\${SERVICES[\$i]}"
done

exit 0
EOF
    chmod 755 "${SCRIPTS_DIR}/preinstall"

    cat > "${SCRIPTS_DIR}/postinstall" <<EOF
#!/usr/bin/env bash
set -e

APPID="${APPID}"
SERVICES=(${SERVICES[*]})

# Lock down ownership/perms on plists and per-service config/log dirs.
chown root:wheel /Library/LaunchDaemons/\${APPID}.*.plist
chmod 644 /Library/LaunchDaemons/\${APPID}.*.plist

for svc in "\${SERVICES[@]}"; do
    CONFIG_DIR="/Library/Application Support/\${APPID}.\${svc}"
    LOG_DIR="/var/log/\${APPID}.\${svc}"
    chown -R root:wheel "\${CONFIG_DIR}" "\${LOG_DIR}"
    chmod 755 "\${CONFIG_DIR}" "\${LOG_DIR}"
    chmod 644 "\${CONFIG_DIR}/config.json"
    xattr -c "\${CONFIG_DIR}/config.json" 2>/dev/null || true
done

# Bootstrap services in dependency order: producers first.
for svc in "\${SERVICES[@]}"; do
    PLIST="/Library/LaunchDaemons/\${APPID}.\${svc}.plist"
    if launchctl bootstrap system "\${PLIST}"; then
        echo "Started \${svc}"
    else
        echo "Failed to start \${svc}"
    fi
done

HELPER_DIR="/usr/local/share/\${APPID}"
echo "============================================"
echo "qcontrollerd installation complete."
echo "Services running on:"
echo "  qemu          → localhost:${QEMU_PORT}"
echo "  eventservice  → localhost:${EVENTSERVICE_PORT}"
echo "  fileregistry  → localhost:${FILEREGISTRY_PORT}"
echo "  controller    → localhost:${CONTROLLER_PORT}"
echo "  orchestrator  → http://localhost:${ORCHESTRATOR_PORT}/ui/"
echo ""
echo "Configs are at /Library/Application Support/\${APPID}.<service>/config.json"
echo ""
echo "If the qemu service can't find qemu/qemu-img/mkisofs on its PATH"
echo "(/usr/bin:/bin:/usr/sbin:/sbin), pin absolute paths in the qemu"
echo "config under a 'binaries' block, then reload:"
echo "  sudo launchctl kickstart -k system/\${APPID}.qemu"
echo ""
echo "To enable OIDC auth, edit the orchestrator config and add an \\\`auth\\\` block."
echo "To uninstall: sudo \${HELPER_DIR}/uninstall.sh"
echo "============================================"
exit 0
EOF
    chmod 755 "${SCRIPTS_DIR}/postinstall"
}

build_pkg() {
    local PKG_NAME="qcontrollerd.pkg"
    pkgbuild --root "${ROOT_DIR}" \
        --identifier "${APPID}" \
        --ownership recommended \
        --version "${PKG_VERSION}" \
        --install-location "/" \
        --scripts "${SCRIPTS_DIR}" \
        "${OUT_DIR}/${PKG_NAME}"

    echo "Package built at: ${OUT_DIR}/${PKG_NAME}"
}

setup_binary
create_all_plists
create_scripts
setup_uninstall_script
build_pkg
