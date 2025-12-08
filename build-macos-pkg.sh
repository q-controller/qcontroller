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

QEMU_CONFIG='{
    "port": "8008"
}'

CONTROLLER_CONFIG='{
    "port": 8009,
    "cache": {
        "root": "cache"
    },
    "root": "USER_HOME_PLACEHOLDER/Library/Application Support/com.github.qcontroller.qcontrollerd.controller/data",
    "qemuEndpoint": "localhost:8008"
}'

GATEWAY_CONFIG='{
    "port": 8080,
    "controllerEndpoint": "localhost:8009",
    "exposeSwaggerUi": true
}'

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
Usage: $(basename "${BASH_SOURCE[0]}") [-h] [-v] -o out_dir
Build script.
Available options:
-h, --help      Print this help and exit
-v, --verbose   Print script debug info
-o, --out       Output directory for built binaries [default: ${OUT_DIR}]
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
    return 0
}

parse_params "$@"

function create_plist() {
    local SERVICE="$1"
    local CONFIG_CONTENT="$2"
    local IS_ROOT="${3:-false}"

    if [[ "$IS_ROOT" == "true" ]]; then
        PLIST_DIR="${ROOT_DIR}/Library/LaunchDaemons"
        CONFIG_BASE="/Library/Application Support"
        LOG_BASE="/var/log"
        CONFIG_DIR="${ROOT_DIR}${CONFIG_BASE}/${APPID}.${SERVICE}"
        LOG_DIR="${ROOT_DIR}${LOG_BASE}/${APPID}.${SERVICE}"
    else
        PLIST_DIR="${ROOT_DIR}/Library/LaunchAgents"
        CONFIG_BASE="USER_HOME_PLACEHOLDER/Library/Application Support"
        LOG_BASE="USER_HOME_PLACEHOLDER/Library/Logs"
        CONFIG_DIR=""
        LOG_DIR=""
    fi

    mkdir -p "${PLIST_DIR}"

    local DYNAMIC_PATH="$PATH"
    for brew_path in "/opt/homebrew/bin" "/opt/homebrew/sbin" "/usr/local/bin" "/usr/local/sbin"; do
        if [[ -d "$brew_path" && ":$DYNAMIC_PATH:" != *":$brew_path:"* ]]; then
            DYNAMIC_PATH="$brew_path:$DYNAMIC_PATH"
        fi
    done
    for sys_path in "/usr/bin" "/bin" "/usr/sbin" "/sbin"; do
        if [[ ":$DYNAMIC_PATH:" != *":$sys_path:"* ]]; then
            DYNAMIC_PATH="$sys_path:$DYNAMIC_PATH"
        fi
    done

    cat <<EOF > "${PLIST_DIR}/${APPID}.${SERVICE}.plist"
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
        <string>${CONFIG_BASE}/${APPID}.${SERVICE}/config.json</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${DYNAMIC_PATH}</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${LOG_BASE}/${APPID}.${SERVICE}/${SERVICE}.out</string>
    <key>StandardErrorPath</key>
    <string>${LOG_BASE}/${APPID}.${SERVICE}/${SERVICE}.err</string>
</dict>
</plist>
EOF

    # Set recommended permissions on the plist (root:wheel, 644)
    chmod 644 "${PLIST_DIR}/${APPID}.${SERVICE}.plist"

    # Create config/log dirs only for system service
    if [[ -n "$CONFIG_DIR" ]]; then
        mkdir -p "${CONFIG_DIR}" "${LOG_DIR}"
        echo "${CONFIG_CONTENT}" > "${CONFIG_DIR}/config.json"
    fi
}

function create_all_plists() {
    create_plist "qemu" "$QEMU_CONFIG" "true"
    create_plist "controller" "$CONTROLLER_CONFIG" "false"
    create_plist "gateway" "$GATEWAY_CONFIG" "false"
}

function setup_binary() {
    ${script_dir}/build.sh "${OUT_DIR}"
    BIN_DIR="${ROOT_DIR}/usr/local/bin"
    mkdir -p "${BIN_DIR}"
    cp "${OUT_DIR}/qcontrollerd" "${BIN_DIR}/qcontrollerd"
    chmod 755 "${BIN_DIR}/qcontrollerd"
}

function setup_uninstall_script() {
    UNINSTALLER_DIR="${ROOT_DIR}/usr/local/share/${APPID}"
    mkdir -p "${UNINSTALLER_DIR}"
    cat > "${UNINSTALLER_DIR}/uninstall.sh" <<'EOF'
#!/usr/bin/env bash
set -e

APPID="com.github.qcontroller.qcontrollerd"
HELPER_DIR="/usr/local/share/${APPID}"

SCRIPT_PATH="$(realpath "$0")"
SCRIPT_DIR="$(dirname "$SCRIPT_PATH")"
if [[ "$SCRIPT_DIR" != "$HELPER_DIR" ]]; then
    echo "Running uninstall from a wrong location: $SCRIPT_DIR"
    exit 1
fi

echo "Uninstalling qcontrollerd..."

# Function to unload and remove a service
unload_service() {
    local SERVICE="$1"
    local IS_ROOT="${2:-false}"
    local LABEL="${APPID}.${SERVICE}"

    if [[ "$IS_ROOT" == "true" ]]; then
        PLIST="/Library/LaunchDaemons/${LABEL}.plist"
        CONFIG_DIR="/Library/Application Support/${LABEL}"
        LOG_DIR="/var/log/${LABEL}"
        DOMAIN="system"
    else
        PLIST="/Library/LaunchAgents/${LABEL}.plist"
        # We don't know which users have configs — remove plist globally, configs per known user later
        DOMAIN="gui/*"  # Placeholder — we'll handle unloading separately
    fi

    # Unload if loaded
    if [[ "$IS_ROOT" == "true" ]]; then
        if launchctl print "system/${LABEL}" >/dev/null 2>&1; then
            echo "Stopping system service: $LABEL"
            sudo launchctl bootout system "$PLIST" || true
        fi
    else
        # Best-effort unload for any user domain that might have it loaded
        for uid in $(ps -eo uid,comm | awk '/qcontrollerd/ {print $1}' | sort -u); do
            if [[ "$uid" != "0" ]]; then  # Skip root processes
                echo "Stopping user service for UID: $uid"
                sudo -u "#$uid" launchctl bootout "gui/$uid" "$PLIST" 2>/dev/null || true
            fi
        done
    fi

    # Fallback: directly kill any remaining processes
    echo "Killing any remaining $SERVICE processes..."
    sudo pkill -f "qcontrollerd $SERVICE" 2>/dev/null || true

    # Remove plist (requires sudo for both Daemons and Agents in /Library)
    sudo rm -f "$PLIST"
    echo "Removed $SERVICE plist"
}

# Stop and remove services (order: gateway → controller → qemu)
unload_service "gateway" "false"
unload_service "controller" "false"
unload_service "qemu" "true"

# Remove binary
sudo rm -f /usr/local/bin/qcontrollerd

# Remove system config/log dirs
sudo rm -rf "/Library/Application Support/${APPID}.qemu"
sudo rm -rf "/var/log/${APPID}.qemu"

# Remove user-specific directories — try known locations
echo "Removing user data directories..."
for user_home in /Users/*; do
    if [[ -d "$user_home" ]]; then
        sudo rm -rf "$user_home/Library/Application Support/${APPID}.controller" 2>/dev/null || true
        sudo rm -rf "$user_home/Library/Application Support/${APPID}.gateway" 2>/dev/null || true
        sudo rm -rf "$user_home/Library/Logs/${APPID}.controller" 2>/dev/null || true
        sudo rm -rf "$user_home/Library/Logs/${APPID}.gateway" 2>/dev/null || true
    fi
done

sudo pkgutil --forget "${APPID}" 2>/dev/null || true

rm -f "$0" 2>/dev/null || true
sudo rm -rf "$HELPER_DIR"

echo "============================================"
echo "qcontrollerd has been completely uninstalled."
echo "All services stopped, files removed."
echo "============================================"

EOF
    chmod 755 "${UNINSTALLER_DIR}/uninstall.sh"
}

function create_scripts() {
    mkdir -p "${SCRIPTS_DIR}"

    cat <<'EOF' > "${SCRIPTS_DIR}/preinstall"
#!/usr/bin/env bash
set -e

APPID="com.github.qcontroller.qcontrollerd"

function unload_service() {
    local LABEL="$1"
    local DOMAIN="$2"
    local PLIST_PATH="$3"

    if launchctl print "${DOMAIN}/${LABEL}" >/dev/null 2>&1; then
        echo "Stopping and removing existing service: $LABEL"
        launchctl bootout "${DOMAIN}/${LABEL}" "${PLIST_PATH}" || true
    fi
    rm -f "${PLIST_PATH}"
}

# System service
unload_service "${APPID}.qemu" "system" "/Library/LaunchDaemons/${APPID}.qemu.plist"

# User services (best-effort, may not exist yet)
unload_service "${APPID}.controller" "gui/$(id -u)" "/Library/LaunchAgents/${APPID}.controller.plist" || true
unload_service "${APPID}.gateway" "gui/$(id -u)" "/Library/LaunchAgents/${APPID}.gateway.plist" || true

exit 0
EOF
    chmod 755 "${SCRIPTS_DIR}/preinstall"

    cat <<'EOF' > "${SCRIPTS_DIR}/postinstall"
#!/usr/bin/env bash
set -e

APPID="com.github.qcontroller.qcontrollerd"

# Reliable logged-in user detection
CONSOLE_USER=$(scutil <<< "show State:/Users/ConsoleUser" | awk '/Name :/ && !/^Name : _mbsetupuser$/ { print $3 }')
if [[ -z "$CONSOLE_USER" || "$CONSOLE_USER" == "loginwindow" ]]; then
    echo "No GUI user logged in - user services will start on next login"
    LOAD_USER_SERVICES=false
else
    LOAD_USER_SERVICES=true
    USER_UID=$(id -u "$CONSOLE_USER")
    USER_HOME=$(dscl . -read "/Users/$CONSOLE_USER" NFSHomeDirectory | awk '{print $2}')
fi

# Replace placeholder in user plists
sed -i '' "s|USER_HOME_PLACEHOLDER|$USER_HOME|g" /Library/LaunchAgents/${APPID}.controller.plist
sed -i '' "s|USER_HOME_PLACEHOLDER|$USER_HOME|g" /Library/LaunchAgents/${APPID}.gateway.plist

# Set proper permissions on all plists
chown root:wheel /Library/LaunchDaemons/${APPID}.qemu.plist /Library/LaunchAgents/${APPID}.*.plist
chmod 644 /Library/LaunchDaemons/${APPID}.qemu.plist /Library/LaunchAgents/${APPID}.*.plist

function setup_service() {
    local SERVICE="$1"
    local IS_ROOT="$2"

    if [[ "$IS_ROOT" == "true" ]]; then
        PLIST="/Library/LaunchDaemons/${APPID}.${SERVICE}.plist"
        CONFIG_DIR="/Library/Application Support/${APPID}.${SERVICE}"
        LOG_DIR="/var/log/${APPID}.${SERVICE}"
    else
        PLIST="/Library/LaunchAgents/${APPID}.${SERVICE}.plist"
        CONFIG_DIR="$USER_HOME/Library/Application Support/${APPID}.${SERVICE}"
        LOG_DIR="$USER_HOME/Library/Logs/${APPID}.${SERVICE}"
    fi

    mkdir -p "$CONFIG_DIR" "$LOG_DIR"

    if [[ "$IS_ROOT" == "true" ]]; then
        chown root:wheel "$CONFIG_DIR" "$LOG_DIR" "$CONFIG_DIR/config.json"
        chmod 755 "$CONFIG_DIR" "$LOG_DIR"
        chmod 644 "$CONFIG_DIR/config.json"
        xattr -c "$CONFIG_DIR/config.json" 2>/dev/null || true

        launchctl bootstrap system "$PLIST" && echo "System service $SERVICE started" || echo "Failed to start system service $SERVICE"
    else
        chmod 755 "$CONFIG_DIR" "$LOG_DIR"
        chown "$CONSOLE_USER:staff" "$CONFIG_DIR" "$LOG_DIR"

        # Create config only if missing
        if [ ! -f "$CONFIG_DIR/config.json" ]; then
            case "$SERVICE" in
                controller) cat <<'CTRL' > "$CONFIG_DIR/config.json"
{
    "port": 8009,
    "cache": { "root": "cache" },
    "root": "$USER_HOME/Library/Application Support/com.github.qcontroller.qcontrollerd.controller/data",
    "qemuEndpoint": "localhost:8008"
}
CTRL
                    ;;
                gateway) cat <<'GATE' > "$CONFIG_DIR/config.json"
{
    "port": 8080,
    "controllerEndpoint": "localhost:8009",
    "exposeSwaggerUi": true
}
GATE
                    ;;
            esac
            # Replace $USER_HOME in the just-written controller config
            [[ "$SERVICE" == "controller" ]] && sed -i '' "s|\$USER_HOME|$USER_HOME|g" "$CONFIG_DIR/config.json"
            chmod 644 "$CONFIG_DIR/config.json"
            chown "$CONSOLE_USER:staff" "$CONFIG_DIR/config.json"
        fi

        if $LOAD_USER_SERVICES; then
            launchctl bootstrap "gui/$USER_UID" "$PLIST" && echo "User service $SERVICE started" || echo "Failed to start user service $SERVICE"
        else
            echo "User service $SERVICE configured - will start on next login"
        fi
    fi
}

# Setup services
setup_service "qemu" "true"
setup_service "controller" "false"
setup_service "gateway" "false"

# Helper script remains useful for manual reloads
HELPER_DIR="/usr/local/share/${APPID}"
mkdir -p "$HELPER_DIR"
cat > "$HELPER_DIR/load-user-services.sh" <<'HELPER'
#!/usr/bin/env bash
APPID="com.github.qcontroller.qcontrollerd"
UID=$(id -u)
echo "Loading qcontrollerd user services for UID $UID..."
for svc in controller gateway; do
    plist="/Library/LaunchAgents/${APPID}.${svc}.plist"
    if [ -f "$plist" ]; then
        launchctl bootstrap "gui/$UID" "$plist" && echo "${svc^} service loaded" || echo "Failed to load $svc"
    fi
done
HELPER
chmod 755 "$HELPER_DIR/load-user-services.sh"

echo "============================================"
echo "qcontrollerd Installation Complete"
echo "============================================"
if $LOAD_USER_SERVICES; then
    echo "All services started."
else
    echo "System service started. User services will start on next login."
    echo "Or run: sudo -u $CONSOLE_USER $HELPER_DIR/load-user-services.sh"
fi
echo "To uninstall in the future, run:"
echo "  sudo ${HELPER_DIR}/uninstall.sh"
echo ""
echo "============================================"
exit 0
EOF
    chmod 755 "${SCRIPTS_DIR}/postinstall"
}

function build_pkg() {
    PKG_NAME="qcontrollerd.pkg"
    pkgbuild --root "${ROOT_DIR}" \
        --identifier "${APPID}" \
        --ownership recommended \
        --version "0.0.1" \
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
