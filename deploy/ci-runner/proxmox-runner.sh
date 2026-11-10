#!/usr/bin/env bash
# Creates a Debian 12 LXC on a Proxmox VE host and registers GitHub Actions
# self-hosted runners in it, so CI runs without paid GitHub-hosted minutes.
#
# Run on the Proxmox host as root:
#   TOKEN=<registration token> bash proxmox-runner.sh
# The token comes from the repo: Settings -> Actions -> Runners -> New
# self-hosted runner (it is valid for one hour). Then set the repository
# variable CI_RUNNER to "self-hosted" so the workflows pick these runners.
#
# Optional environment:
#   CTID=411 HOSTNAME_CT=ci-runner STORAGE=local-lvm DISK_GB=40 CORES=4
#   MEMORY_MB=8192 BRIDGE=vmbr0 IP=dhcp (or 192.168.20.89/24,gw=192.168.20.1)
#   RUNNERS=2 REPO_URL=https://github.com/NeciMNK/my-panel-vpn
set -euo pipefail

CTID="${CTID:-411}"
HOSTNAME_CT="${HOSTNAME_CT:-ci-runner}"
STORAGE="${STORAGE:-local-lvm}"
TEMPLATE_STORAGE="${TEMPLATE_STORAGE:-local}"
DISK_GB="${DISK_GB:-40}"
CORES="${CORES:-4}"
MEMORY_MB="${MEMORY_MB:-8192}"
BRIDGE="${BRIDGE:-vmbr0}"
IP="${IP:-dhcp}"
RUNNERS="${RUNNERS:-2}"
REPO_URL="${REPO_URL:-https://github.com/NeciMNK/my-panel-vpn}"
TOKEN="${TOKEN:-}"

die() {
	echo "error: $*" >&2
	exit 1
}

[ "$(id -u)" -eq 0 ] || die "run as root on the Proxmox host"
command -v pct >/dev/null || die "pct not found: this is not a Proxmox VE host"
[ -n "$TOKEN" ] || die "set TOKEN to a runner registration token"
case "$RUNNERS" in '' | *[!0-9]*) die "RUNNERS must be a number" ;; esac

if pct status "$CTID" >/dev/null 2>&1; then
	die "container $CTID already exists; pick another CTID or remove it first"
fi

echo "==> Looking up the Debian 12 template"
pveam update >/dev/null
template="$(pveam available --section system | awk '/debian-12-standard/ {print $2}' | sort -V | tail -n1)"
[ -n "$template" ] || die "no debian-12-standard template available"
if ! pveam list "$TEMPLATE_STORAGE" | grep -q "$template"; then
	pveam download "$TEMPLATE_STORAGE" "$template"
fi

if [ "$IP" = "dhcp" ]; then
	net="name=eth0,bridge=$BRIDGE,ip=dhcp"
else
	net="name=eth0,bridge=$BRIDGE,ip=$IP"
fi

echo "==> Creating container $CTID ($HOSTNAME_CT)"
# nesting + keyctl let Docker run inside the unprivileged container; CI's
# PostgreSQL jobs start postgres:16 as a service container.
pct create "$CTID" "$TEMPLATE_STORAGE:vztmpl/$template" \
	--hostname "$HOSTNAME_CT" \
	--unprivileged 1 \
	--features nesting=1,keyctl=1 \
	--cores "$CORES" \
	--memory "$MEMORY_MB" \
	--swap 2048 \
	--rootfs "$STORAGE:$DISK_GB" \
	--net0 "$net" \
	--onboot 1
pct start "$CTID"

echo "==> Waiting for the network"
for _ in $(seq 1 60); do
	if pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1; then
		break
	fi
	sleep 2
done
pct exec "$CTID" -- getent hosts github.com >/dev/null || die "container has no DNS/network"

echo "==> Installing packages and Docker"
# shellcheck disable=SC2016 # expanded inside the container, not here
pct exec "$CTID" -- bash -euo pipefail -c '
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl git jq sudo build-essential unzip xz-utils zstd shellcheck
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
	>/etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io
systemctl enable --now docker
id runner >/dev/null 2>&1 || useradd -m -s /bin/bash runner
usermod -aG docker runner
echo "runner ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/runner
chmod 0440 /etc/sudoers.d/runner
'

echo "==> Installing $RUNNERS GitHub Actions runner(s)"
# shellcheck disable=SC2016 # expanded inside the container, not here
pct exec "$CTID" -- env TOKEN="$TOKEN" REPO_URL="$REPO_URL" RUNNERS="$RUNNERS" HOSTNAME_CT="$HOSTNAME_CT" bash -euo pipefail -c '
version="$(curl -fsSL https://api.github.com/repos/actions/runner/releases/latest | jq -r .tag_name | sed s/^v//)"
[ -n "$version" ] && [ "$version" != null ] || { echo "cannot resolve the runner version" >&2; exit 1; }
archive="/tmp/actions-runner-$version.tar.gz"
curl -fsSL -o "$archive" "https://github.com/actions/runner/releases/download/v$version/actions-runner-linux-x64-$version.tar.gz"
for i in $(seq 1 "$RUNNERS"); do
	dir="/home/runner/actions-runner-$i"
	mkdir -p "$dir"
	tar -xzf "$archive" -C "$dir"
	chown -R runner:runner "$dir"
	"$dir/bin/installdependencies.sh" >/dev/null
	sudo -u runner "$dir/config.sh" --unattended --replace \
		--url "$REPO_URL" --token "$TOKEN" \
		--name "$HOSTNAME_CT-$i" --labels self-hosted,linux,x64,proxmox \
		--work _work
	(cd "$dir" && ./svc.sh install runner && ./svc.sh start)
done
rm -f "$archive"
'

echo
echo "Done. Runners show up under Settings -> Actions -> Runners."
echo "Next: Settings -> Secrets and variables -> Actions -> Variables ->"
echo "      New repository variable  CI_RUNNER = self-hosted"
