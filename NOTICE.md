# Notice

**my-panel-vpn** is a modified version of [3X-UI](https://github.com/MHSanaei/3x-ui).

- Original work: Copyright (C) the 3X-UI contributors
  (<https://github.com/MHSanaei/3x-ui/graphs/contributors>), which itself
  continues the X-UI project.
- Modifications: Copyright (C) 2026 NeciMNK and the my-panel-vpn contributors.

Both the original work and the modifications are distributed under the
**GNU General Public License, version 3** (GPL-3.0). The full text is in
[`LICENSE`](LICENSE). This program comes with ABSOLUTELY NO WARRANTY, to the
extent permitted by applicable law (GPL-3.0 sections 15 and 16).

## What was modified

This fork keeps the full upstream git history. Every change made after
importing 3X-UI, with its date and author, is recorded in that history; the
first fork commits are dated 2026-11-09. Compared to upstream, the fork adds
node configuration profiles pushed from the master, one-command node setup,
cascades, a per-country speed balancer, exit stickiness, per-user speed
limits, home-network tunnels with access rules, a WARP toggle, multi-master
failover, a migration importer and related CI changes. See
[`FORK.ru.md`](FORK.ru.md) (Russian) where present for the roadmap.

The Go module path is still `github.com/mhsanaei/3x-ui/v3` so upstream
changes keep merging cleanly; this is not an official 3X-UI release, and the
3X-UI maintainers are not responsible for it.

## Remnawave

No source code from [Remnawave](https://github.com/remnawave) (AGPL-3.0) is
included. The fork only borrows product ideas (master-managed node configs,
squads, one-command node install) and implements them independently in Go.
The migration importer reads the JSON that Remnawave's public REST API
returns, which is interoperability, not copying.

## Third-party components

Third-party components keep their own licenses. Go dependencies are listed in
[`go.mod`](go.mod) and frontend/docs dependencies in their
`package-lock.json` / `pnpm-lock.yaml`. Components worth naming because they
are fetched or bundled at build time:

| Component | License |
| --- | --- |
| [Xray-core](https://github.com/XTLS/Xray-core) (binary in release archives and as a Go library) | MPL-2.0 |
| [mtg-multi](https://github.com/mhsanaei/mtg-multi) (MTProto sidecar binary) | see its repository |
| [amneziawg-go](https://github.com/amnezia-vpn/amneziawg-go), [wireguard-go](https://git.zx2c4.com/wireguard-go) | MIT |
| [gVisor](https://github.com/google/gvisor) netstack | Apache-2.0 |
| geoip/geosite data from [Loyalsoldier](https://github.com/Loyalsoldier/v2ray-rules-dat), [chocolate4u](https://github.com/chocolate4u/Iran-v2ray-rules), [runetfreedom](https://github.com/runetfreedom/russia-v2ray-rules-dat) | GPL-3.0 |

## Trademarks

Names and icons of client apps shown on the subscription page (Happ, INCY,
Shadowrocket, sing-box, Streisand, V2Box, v2rayNG, v2RayTun and others)
belong to their owners and are used only to identify those apps. "3X-UI",
"Xray" and "Remnawave" are the names of their respective projects; this fork
is not affiliated with or endorsed by them.
