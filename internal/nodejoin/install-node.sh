#!/usr/bin/env bash
# Turns this server into a node of a my-panel-vpn master. Served by the master:
#   curl -fsSL <master>/node-join/install.sh | bash -s -- --master <master> --join <token>
set -euo pipefail

MASTER=""
JOIN=""
ADDRESS=""
PANEL_PORT=""
NAME=""
COUNTRY=""
INSECURE=0
REPLACE=0
XUI_DIR="${XUI_MAIN_FOLDER:-/usr/local/x-ui}"
CERT_DIR="/etc/x-ui/node-cert"

# Names shared with install.sh, whose panel-replacement functions are copied below.
xui_repo="NeciMNK/my-panel-vpn"
xui_folder="$XUI_DIR"
xui_service="/etc/systemd/system"
xui_cli="/usr/bin/x-ui"
xui_rc_script="/etc/init.d/x-ui"
xui_etc="/etc/x-ui"
xui_backup_root="/root"
release="$(. /etc/os-release 2>/dev/null && echo "${ID:-}" || true)"
replace_existing_xui=0

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

die() {
    echo -e "${red}error:${plain} $*" >&2
    exit 1
}
info() { echo -e "${green}==>${plain} $*"; }

usage() {
    cat <<USAGE
Usage: install-node.sh --master <url> --join <token> [options]
  --master URL     master panel URL including its base path
  --join TOKEN     one-time join token from the master's Nodes page
  --address HOST   address the master should use for this node (default: the IP the master sees)
  --port PORT      panel port for this node (default: random free port)
  --name NAME      node name on the master (default: hostname)
  --country CC     two-letter country code, joins that country's cascades (e.g. DE)
  --insecure       accept a self-signed certificate on the master
  --replace        back up and replace a full my-panel-vpn panel already on this server
USAGE
}

while [[ $# -gt 0 ]]; do
    case "$1" in
    --master) MASTER="${2:-}"; shift 2 ;;
    --join) JOIN="${2:-}"; shift 2 ;;
    --address) ADDRESS="${2:-}"; shift 2 ;;
    --port) PANEL_PORT="${2:-}"; shift 2 ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --country) COUNTRY="${2:-}"; shift 2 ;;
    --insecure) INSECURE=1; shift ;;
    --replace) REPLACE=1; shift ;;
    -h | --help) usage; exit 0 ;;
    *) usage; die "unknown option: $1" ;;
    esac
done

[[ -n "$MASTER" && -n "$JOIN" ]] || { usage; die "--master and --join are required"; }
[[ $EUID -eq 0 ]] || die "run as root"
[[ "$JOIN" =~ ^[A-Za-z0-9_-]+$ ]] || die "malformed join token"
MASTER="${MASTER%/}"

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
armv7* | armv7l) ARCH=armv7 ;;
*) die "unsupported CPU architecture: $(uname -m)" ;;
esac

for bin in curl tar openssl systemctl; do
    command -v "$bin" >/dev/null 2>&1 || die "$bin is required"
done

CURL=(curl -fsSL --retry 3 --connect-timeout 15)
[[ $INSECURE -eq 1 ]] && CURL+=(-k)

rand_hex() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }

port_in_use() {
    if command -v ss >/dev/null 2>&1; then
        ss -ltnH "sport = :$1" 2>/dev/null | grep -q .
    else
        (echo >/dev/tcp/127.0.0.1/"$1") >/dev/null 2>&1
    fi
}

# Copies of install.sh (TestInstallNodeSharesPanelReplacementWithInstaller pins them).
xui_env_file_path() {
    case "${release}" in
        ubuntu | debian | armbian) echo "/etc/default/x-ui" ;;
        arch | manjaro | parch | alpine) echo "/etc/conf.d/x-ui" ;;
        *) echo "/etc/sysconfig/x-ui" ;;
    esac
}

# Prints none, fork or foreign. Upstream 3x-ui (and other x-ui builds) share
# every path with this fork; only this fork's binary or CLI names its repo.
detect_xui_install() {
    local f found=0
    for f in "${xui_folder}/x-ui" "${xui_cli}" "${xui_service}/x-ui.service" "${xui_rc_script}"; do
        [[ -e "$f" ]] || continue
        found=1
        if [[ "$f" == "${xui_folder}/x-ui" || "$f" == "${xui_cli}" ]] && grep -aqF "${xui_repo}" "$f" 2> /dev/null; then
            echo fork
            return 0
        fi
    done
    if [[ $found == 1 ]]; then echo foreign; else echo none; fi
}

# Stops the foreign panel, copies its database, env file and unit aside, and only
# then removes it; a failed copy restarts it and returns 1 with nothing deleted.
remove_foreign_xui() {
    local env_file db_folder backup
    env_file="$(xui_env_file_path)"
    db_folder="$(sed -n 's/^XUI_DB_FOLDER=["'\'']\{0,1\}\([^"'\'']*\).*/\1/p' "${env_file}" 2> /dev/null | tail -n1)"
    db_folder="${db_folder:-${xui_etc}}"
    backup="${xui_backup_root}/3x-ui-backup-$(date +%Y%m%d-%H%M%S)"

    if [[ $release == "alpine" ]]; then
        rc-service x-ui stop > /dev/null 2>&1
    else
        systemctl stop x-ui > /dev/null 2>&1
    fi
    pkill -f 'mtg-linux-[^ ]* run ' > /dev/null 2>&1 || true
    pkill -f 'tuic-server.*-c .*bin/tuic/tuic_[0-9]+\.json' > /dev/null 2>&1 || true

    local ok=1
    mkdir -p "${backup}" || ok=0
    if [[ $ok == 1 && -d "${db_folder}" ]]; then cp -a "${db_folder}" "${backup}/x-ui" || ok=0; fi
    if [[ $ok == 1 && -f "${env_file}" ]]; then cp -a "${env_file}" "${backup}/x-ui.env" || ok=0; fi
    if [[ $ok == 1 && -f "${xui_service}/x-ui.service" ]]; then cp -a "${xui_service}/x-ui.service" "${backup}/" || ok=0; fi
    if [[ $ok == 1 && -d "${xui_service}/x-ui.service.d" ]]; then cp -a "${xui_service}/x-ui.service.d" "${backup}/" || ok=0; fi
    if [[ $ok == 1 && -f "${xui_rc_script}" ]]; then cp -a "${xui_rc_script}" "${backup}/x-ui.rc" || ok=0; fi
    if [[ $ok != 1 ]]; then
        echo -e "${red}Could not back up the existing panel to ${backup}; nothing was removed. Free some disk space and run the installer again.${plain}"
        if [[ $release == "alpine" ]]; then
            rc-service x-ui start > /dev/null 2>&1
        else
            systemctl start x-ui > /dev/null 2>&1
        fi
        return 1
    fi

    if [[ $release == "alpine" ]]; then
        rc-update del x-ui > /dev/null 2>&1
        rm -f "${xui_rc_script}"
    else
        systemctl disable x-ui > /dev/null 2>&1
        rm -f "${xui_service}/x-ui.service"
        rm -rf "${xui_service}/x-ui.service.d"
        systemctl daemon-reload > /dev/null 2>&1
        systemctl reset-failed x-ui > /dev/null 2>&1
    fi
    # A custom XUI_DB_FOLDER is only backed up: the env file pointing at it goes away.
    rm -rf "${xui_folder}" "${xui_etc}"
    rm -f "${xui_cli}" "${env_file}"

    echo -e "${green}Old panel removed. Backup: ${backup}${plain}"
    if [[ -f "${backup}/x-ui/x-ui.db" ]]; then
        echo -e "${green}To bring its inbounds and clients over, upload ${backup}/x-ui/x-ui.db in the master panel: Move from another panel → 3x-ui.${plain}"
    fi
    if grep -q '^XUI_DB_TYPE=postgres' "${backup}/x-ui.env" 2> /dev/null; then
        echo -e "${yellow}The old panel used PostgreSQL; its database was left as is (connection settings are in ${backup}/x-ui.env).${plain}"
    fi
    return 0
}

# A node's state comes from its master, so any panel or database already here is
# backed up and replaced; only a full panel of this fork needs --replace.
check_existing_panel() {
    local kind
    kind="$(detect_xui_install)"
    if [[ "$kind" == none && ! -e "$xui_etc" && ! -e "$(xui_env_file_path)" ]]; then
        return 0
    fi
    if [[ "$kind" == fork && "$REPLACE" != 1 ]] && ! grep -qs '^Description=x-ui node of ' "${xui_service}/x-ui.service"; then
        die "this server already runs a ${xui_repo} panel; rerun with --replace to back it up and turn it into a node"
    fi
    info "An existing x-ui panel or database was found: it will be backed up to ${xui_backup_root}/3x-ui-backup-<date>/ and replaced"
    replace_existing_xui=1
}

check_existing_panel

if [[ -z "$PANEL_PORT" ]]; then
    for _ in $(seq 1 50); do
        candidate=$((20000 + RANDOM % 40000))
        if ! port_in_use "$candidate"; then
            PANEL_PORT=$candidate
            break
        fi
    done
    [[ -n "$PANEL_PORT" ]] || die "no free port found, pass --port"
fi
[[ "$PANEL_PORT" =~ ^[0-9]+$ && "$PANEL_PORT" -ge 1 && "$PANEL_PORT" -le 65535 ]] || die "invalid --port"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

info "Downloading the panel build from the master"
"${CURL[@]}" -o "$TMP/bundle.tar.gz" "$MASTER/node-join/bundle?join=$JOIN&arch=$ARCH" ||
    die "download failed: the token may be expired or used, or this server's CPU architecture differs from the master's"

if [[ $replace_existing_xui == 1 ]]; then
    remove_foreign_xui || die "could not back up the existing panel; it was left running"
fi

mkdir -p "$XUI_DIR"
tar -xzf "$TMP/bundle.tar.gz" -C "$XUI_DIR"
chmod +x "$XUI_DIR/x-ui"
find "$XUI_DIR/bin" -maxdepth 1 -type f \( -name 'xray-*' -o -name 'mtg*' -o -name 'tuic*' \) -exec chmod +x {} +
if [[ -f "$XUI_DIR/x-ui.sh" ]]; then
    install -m 0755 "$XUI_DIR/x-ui.sh" /usr/bin/x-ui
fi

cat >/etc/systemd/system/x-ui.service <<UNIT
[Unit]
Description=x-ui node of ${MASTER}
After=network.target
Wants=network.target

[Service]
Type=simple
WorkingDirectory=${XUI_DIR}/
ExecStart=${XUI_DIR}/x-ui
Restart=on-failure
RestartSec=5s
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
UNIT

info "Configuring the node panel"
cd "$XUI_DIR"
BASE_PATH="/$(rand_hex 9)/"
ADMIN_USER="admin-$(rand_hex 3)"
ADMIN_PASS="$(rand_hex 12)"
./x-ui setting -port "$PANEL_PORT" -webBasePath "$BASE_PATH" -username "$ADMIN_USER" -password "$ADMIN_PASS" >/dev/null

mkdir -p "$CERT_DIR"
chmod 700 "$CERT_DIR"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
    -subj "/CN=x-ui-node" -keyout "$CERT_DIR/key.pem" -out "$CERT_DIR/cert.pem" >/dev/null 2>&1 ||
    die "could not create the node TLS certificate"
chmod 600 "$CERT_DIR/key.pem"
./x-ui cert -webCert "$CERT_DIR/cert.pem" -webCertKey "$CERT_DIR/key.pem" >/dev/null
CERT_PIN="$(openssl x509 -in "$CERT_DIR/cert.pem" -outform der | openssl dgst -sha256 -hex | awk '{print $NF}')"

API_TOKEN="$(./x-ui setting -getApiToken -tokenName master-sync -tokenScope node-sync | awk '/^apiToken:/{print $2}' | tail -n1)"
[[ -n "$API_TOKEN" ]] || die "could not create the node API token"

systemctl daemon-reload
systemctl enable x-ui >/dev/null 2>&1
systemctl restart x-ui

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw allow "$PANEL_PORT"/tcp >/dev/null
fi

[[ -n "$NAME" ]] || NAME="$(hostname -s 2>/dev/null || hostname)"
NAME="$(printf '%s' "$NAME" | tr -cd 'A-Za-z0-9._-' | cut -c1-48)"
ADDRESS="$(printf '%s' "$ADDRESS" | tr -cd 'A-Za-z0-9.:_-')"
COUNTRY="$(printf '%s' "$COUNTRY" | tr -cd 'A-Za-z' | cut -c1-2 | tr 'a-z' 'A-Z')"

info "Registering with the master"
PAYLOAD=$(printf '{"join":"%s","name":"%s","address":"%s","port":%s,"basePath":"%s","apiToken":"%s","certSha256":"%s","country":"%s"}' \
    "$JOIN" "$NAME" "$ADDRESS" "$PANEL_PORT" "$BASE_PATH" "$API_TOKEN" "$CERT_PIN" "$COUNTRY")
RESPONSE="$("${CURL[@]}" -X POST -H 'Content-Type: application/json' -d "$PAYLOAD" "$MASTER/node-join/register")" ||
    die "registration failed"
if ! printf '%s' "$RESPONSE" | grep -q '"success":true'; then
    die "the master refused the registration: $RESPONSE"
fi

NODE_HOST="$(printf '%s' "$RESPONSE" | sed -n 's/.*"address":"\([^"]*\)".*/\1/p')"
[[ -n "$NODE_HOST" ]] || NODE_HOST="$(hostname -I 2>/dev/null | awk '{print $1}')"
[[ "$NODE_HOST" == *:* ]] && NODE_HOST="[${NODE_HOST}]"

echo
echo -e "${green}This server is now a node of ${MASTER}.${plain}"
echo -e "Node panel (for emergencies): https://${NODE_HOST}:${PANEL_PORT}${BASE_PATH}"
echo -e "Login: ${yellow}${ADMIN_USER}${plain}  Password: ${yellow}${ADMIN_PASS}${plain}"
