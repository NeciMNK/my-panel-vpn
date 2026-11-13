#!/usr/bin/env bash
# Under lxcfs the sandbox drop-in must turn ProtectKernelTunables off, or the
# panel's fresh procfs reports the Proxmox host's CPU, memory and uptime.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

load_function() {
    local definition
    definition=$(awk -v name="$2" '
        $0 == name "() {" {copy = 1}
        copy {print}
        copy && /^}$/ {exit}
    ' "$1")
    [[ -n "$definition" ]] || { echo "Missing function $2 in $1" >&2; return 1; }
    eval "$definition"
}

# Mountinfo as an LXC guest, a plain host and a Proxmox host running lxcfs see it.
cat > "$test_dir/lxc-guest" << 'MOUNTS'
1040 1039 0:58 / / rw,relatime master:1 - zfs rpool/data/subvol-410-disk-0 rw,xattr,posixacl
1041 1040 0:62 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
1042 1041 0:63 /proc/meminfo /proc/meminfo rw,nosuid,nodev,relatime master:283 - fuse.lxcfs lxcfs rw,user_id=0,group_id=0,allow_other
1043 1041 0:63 /proc/uptime /proc/uptime rw,nosuid,nodev,relatime master:283 - fuse.lxcfs lxcfs rw,user_id=0,group_id=0,allow_other
MOUNTS
cat > "$test_dir/plain-host" << 'MOUNTS'
22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
23 22 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw
MOUNTS
cat > "$test_dir/lxcfs-host" << 'MOUNTS'
22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
23 22 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw
512 22 0:55 / /var/lib/lxcfs rw,nosuid,nodev,relatime shared:280 - fuse.lxcfs lxcfs rw,user_id=0,group_id=0,allow_other
MOUNTS

# shellcheck disable=SC2317,SC2329
systemctl() {
    [[ "$1" == "--version" ]] && echo "systemd 252 (252.33-1~deb12u1)"
    return 0
}

for script in install.sh update.sh; do
    (
        installer="$repo_root/$script"
        load_function "$installer" _xui_proc_has_lxcfs

        _xui_proc_has_lxcfs "$test_dir/lxc-guest" || fail "$script: lxcfs over /proc not detected"
        ! _xui_proc_has_lxcfs "$test_dir/plain-host" || fail "$script: plain host detected as lxcfs"
        ! _xui_proc_has_lxcfs "$test_dir/lxcfs-host" || fail "$script: lxcfs host detected as a guest"

        load_function "$installer" _xui_systemd_major_version
        load_function "$installer" _xui_service_write_paths_dropin
        if grep -q '^xui_env_file_path() {' "$installer"; then
            load_function "$installer" xui_env_file_path
        fi
        # Variables read by the loaded installer functions.
        # shellcheck disable=SC2034
        release=debian xui_folder=/usr/local/x-ui
        printf '' > "$test_dir/env"

        for mounts in lxc-guest plain-host; do
            xui_service="$test_dir/$script-$mounts"
            # shellcheck disable=SC2317,SC2329
            _xui_proc_has_lxcfs() { [[ "$mounts" == lxc-guest ]]; }
            _xui_service_write_paths_dropin "$test_dir/env" || fail "$script/$mounts: drop-in not written"
            dropin="$xui_service/x-ui.service.d/10-xui-sandbox.conf"
            if [[ "$mounts" == lxc-guest ]]; then
                grep -qx 'ProtectKernelTunables=no' "$dropin" || fail "$script/$mounts: hardening still hides lxcfs"
            elif grep -q '^ProtectKernelTunables=' "$dropin"; then
                fail "$script/$mounts: hardening relaxed outside lxcfs"
            fi
        done
    )
    echo "ok $script"
done
