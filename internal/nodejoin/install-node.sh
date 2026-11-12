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
XUI_DIR="${XUI_MAIN_FOLDER:-/usr/local/x-ui}"
CERT_DIR="/etc/x-ui/node-cert"

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

if systemctl is-active --quiet x-ui 2>/dev/null; then
    info "Stopping the existing x-ui service"
    systemctl stop x-ui
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
# The master serves subscriptions; a node's own sub server only fights for :2096.
./x-ui setting -port "$PANEL_PORT" -webBasePath "$BASE_PATH" -username "$ADMIN_USER" -password "$ADMIN_PASS" -subEnable=false >/dev/null

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
