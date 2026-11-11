# Migration plan: Remnawave → my-panel-vpn

[Русская версия](migration-plan.ru.md) · [Roadmap](plan.md) ·
[Import window reference](docs/content/docs/en/operations/migrate-from-other-panels.mdx)

Goal: move users, nodes, subscriptions and the bot from Remnawave to this
panel without users noticing: same subscription links, same configs in their
apps, no "please refresh your subscription". VPN downtime is 1–3 minutes in
one planned night window.

In short:

| | Time |
| --- | --- |
| Preparation (can be spread over several days, VPN keeps working) | 4–7 hours of work |
| Cutover (the window when VPN is down) | 1–3 minutes of downtime, 30–60 minutes of work in the window |
| Observation, one-command rollback still possible | 1–3 days |
| Removing Remnawave | after 7 days, 30 minutes |

Total: **one working day of hands-on work plus a week of safety net**. Fastest
path: prepare during the day, cut over the same night, watch until the end of
the week.

---

## 0. How it works and why users don't notice

A user's app holds two things: the subscription link and the configs it
fetched. The migration keeps both:

- **Configs.** VLESS UUIDs, Trojan/Shadowsocks passwords, Hysteria2 auth
  (`vlessUuid`), ports, Reality keys and `shortIds`, SNI and host addresses
  are copied as they are. This panel's nodes go **onto the same servers** and
  take **the same ports**, so the old config in the app simply connects to the
  new Xray.
- **Subscription.** The subscription id (`shortUuid`) becomes the client's
  `subId`, and the subscription path and address come from Remnawave's
  `subscriptionUrl`. Once the subscription domain points at this panel, the
  old link returns the same set of configs (15 of 15 subscriptions matched on
  a copy of the live Remnawave, PR #34).
- **The rest.** Traffic limit and usage, expiry, reset strategy, Telegram ID,
  description, HWID device limit, disabled users, config names (`{{HOST}}`
  template), host filters by internal squad.

Remnawave is **only read** during the migration, so rollback is always
possible until the new panel accumulates changes (section 5).

Why the cutover is all at once, not node by node: the import skips clients
that already exist (by email). If one node moved first and a second one later,
the second node's clients would not be attached to it. So every `remnanode`
stops together and the import runs **once**.

## 1. What is not migrated and how to handle it beforehand

| What | What to do | When |
| --- | --- | --- |
| Routing, outbounds and DNS of the Remnawave config profile (torrent block, WARP, geosite rules) | recreate in the Xray template of the master and nodes (**Xray settings** with the node picker on top); WARP via the **Nodes → WARP** toggle | step 2.4 |
| TLS certificates remnanode reads from disk (Hysteria, VLESS/Trojan over TLS), e.g. `/opt/certs/...` | copy them to the same path on the node server; the import window lists every such file | step 2.5 |
| External squads, host xhttp extra params | check whether you use them; if so, redo them by hand after the import | step 2.6 |
| HWID device lists | nothing: the limit is kept, devices register again on the next subscription refresh | — |
| A user with Trojan and Shadowsocks under different passwords | their Shadowsocks config works only after a subscription refresh; the import window names them — tell them in advance or accept one refresh | step 2.6 |
| Subscription title, support link, announcement, update interval, Happ routing profile | set them in **Settings → Subscription** to match Remnawave, otherwise the profile name in apps changes on the next refresh | step 2.7 |
| An external shop bot or payment scripts using the Remnawave API | they stop working; this panel's bot replaces them (step 2.8) | step 2.8 |
| Payment history and balances in an external bot | not imported: paid time is already in `expiryTime`; export the history from the old bot | step 2.8 |

## 2. Preparation (VPN keeps working, users see nothing)

### 2.1. Remnawave inventory — 20–30 minutes

Write down in a private note (not in the repo — it is public):

- the Remnawave panel address, subscription domain and path (one user's link
  as an example);
- the node list: name, address, which inbounds and ports each one runs;
- what the config profile holds besides inbounds: routing, outbounds, DNS;
- where node TLS certificates come from (file paths);
- who else talks to the Remnawave API: shop bot, scripts, monitoring;
- the Telegram bot token and what it does (notifications, sales);
- subscription settings: title, support URL, announce, update interval.

Create a Remnawave API token (**Settings → API Tokens**); the import only reads
with it.

### 2.2. Backups — 15 minutes

On the Remnawave server:

```bash
cd /opt/remnawave
docker compose exec -T remnawave-db pg_dumpall -U postgres > ~/remnawave-$(date +%F).sql
cp .env docker-compose.yml ~/   # plus the Caddy/nginx config if it is separate
```

Copy the files off the server. This covers a rollback a week later going wrong.

### 2.3. This panel's master — 30–60 minutes

Install the master (`install.sh`, PostgreSQL) on a separate server or on the
Remnawave server. On the same server the panel (`2053`) and subscription
(`2096`) ports don't clash with Remnawave (`3000`, `3010`); the subscription
domain is switched later in step 3.4.

Right away:

- change the login, password and panel path, turn on two-factor auth;
- issue a certificate for the subscription domain (**Certificates**,
  Cloudflare DNS); once issued it is put into the subscription settings
  automatically (PR #32);
- turn on the Telegram backup (**Settings → Telegram**).

### 2.4. Xray template: routing and outbounds — 30–90 minutes

Carry over everything from the Remnawave config profile that is not an
inbound: `routing` rules, `outbounds` (WARP, block list, direct), `dns`. Where
the panel has a ready feature, use it (WARP toggle, panel proxy). The rest goes
into **Xray settings** → pick the node (or the master) → rules, outbounds,
DNS. The template lives on the master and is pushed to nodes.

### 2.5. Nodes on the Remnawave servers — 10 minutes per node (5 nodes ≈ 45 minutes)

On every server that runs `remnanode`:

1. **Nodes → Add** on the master, run the command it gives on the server. The
   node panel port is picked free, so `remnanode` and its `NODE_PORT` are not
   touched. The new node has no inbounds yet, so Xray ports stay with
   `remnanode`.
2. Check that the node shows online on the master.
3. If the profile has TLS inbounds, copy the certificates to the same paths
   (`/opt/certs/...`) and make sure the node's service user can read them.
4. Check that the firewall lets the master reach the node panel port.

### 2.6. Dry run on the test stand — 1–2 hours (mandatory)

Take a Remnawave snapshot (script in the
[reference](docs/content/docs/en/operations/migrate-from-other-panels.mdx),
section "Remnawave snapshot file") and import it into the **test** master.
Check:

- **Check** in the import window: every Remnawave node maps to a node of this
  panel, no port conflicts, the whole "Notes" list read;
- 5–10 subscriptions of different users (regular, restricted squads,
  disabled, expired, with Hysteria): content matches Remnawave's answer for
  the same link — `curl` both and `diff` the decoded base64;
- with a test user, a real connection in Happ/v2rayN to the test node using the
  old config.

Reset the test master to an empty database afterwards.

### 2.7. Subscription settings — 15 minutes

On the production master, set **Settings → Subscription** to what Remnawave
had: subscription title, support link, announcement, update interval, Happ
routing profile (if used). Keep "Take over the old subscription paths and
address" on in the import window; it sets the path and `subURI` itself.

### 2.8. Bot and payments — 30–60 minutes

This panel's bot shows clients their expiry, amount and payment details,
takes multi-month payments, "pay later" and the free plan; the admin confirms
a payment with one button. There is no online acquiring (YooKassa, CryptoBot
and so on) — if the external bot had it, this is a deliberate change for
clients.

- **Telegram IDs are imported**, so the bot recognises clients right after the
  import.
- You can keep the same bot (clients don't have to find a new one): at cutover
  stop the old bot and put **the same token** into **Settings → Telegram**.
  Only one process can read a token; if the old bot used a webhook, remove it:
  `curl "https://api.telegram.org/bot<TOKEN>/deleteWebhook"` — the panel bot
  uses long polling.
- Fill in prices, discounts, payment details and the free plan beforehand
  (**Finance**, **Settings → Telegram → Payment**).
- Export payment history and balances from the old bot (if any) — they are not
  imported here. For users with a positive balance, extend their expiry in
  Remnawave by that amount first, then the expiry carries over by itself.
- Move scripts and services that used the Remnawave API to this panel's API
  (`/panel/api/...`, token under **Settings → Security → API tokens**;
  payments: `/panel/api/billing/client` and `/pay`).

### 2.9. DNS in advance — 2 minutes (a day before the window)

If the subscription domain will be switched through DNS (master on another
server), lower the record's TTL to 60 seconds **24 hours** before the window,
or some apps keep hitting the old server for a day.

## 3. Cutover (30–60 minute window, 1–3 minutes of VPN downtime)

Pick the quietest time (night). Have open: the master on the import window, SSH
sessions to every node, Remnawave in another tab.

### 3.1. Final check — 5 minutes

Import window (**Settings → Move from…**) → Remnawave address and API token → **Check**. Nodes mapped, no
port conflicts, warnings match the dry run.

### 3.2. Stop remnanode on every node — 1 minute

On all servers at the same time (e.g. a command pre-typed in every SSH
session):

```bash
cd /opt/remnanode && docker compose down
```

From now on the VPN is down. **Do not** stop the Remnawave panel — the import
reads from it.

### 3.3. Import — 1–2 minutes

Press **Import** right away (subscription paths box on). Inbounds are created
on the nodes, Xray takes the same ports with the same keys; the panel restarts
the subscription server. The VPN works again.

Check the result: "Imported: N inbounds, M clients, K hosts" matches the dry
run, and the "Not imported" list is empty or expected. Pressing **Import**
again duplicates nothing if something has to be pulled in.

### 3.4. Switch the subscription domain — 5–15 minutes

- Master on the Remnawave server: in Caddy/nginx point the subscription
  domain's `reverse_proxy`/`proxy_pass` from `subscription-page`/Remnawave to
  `127.0.0.1:2096` and reload the proxy.
- Master on another server: change the subscription domain's A record to the
  master (TTL is already 60 s), or run the subscription server directly on
  `443` with the domain's certificate.

Then stop the Remnawave and `subscription-page` containers so nobody changes
data there by accident:

```bash
cd /opt/remnawave && docker compose stop
```

### 3.5. Bot — 5 minutes

Stop the old bot, `deleteWebhook` if needed, put the token into
**Settings → Telegram**, send `/start` from your account and a test client.

### 3.6. Post-cutover check — 15–30 minutes

- 3–5 real users' links open and return what the dry run did (`curl` + `diff`).
- Your own app connects to every node **without refreshing the subscription**.
- **Nodes**: all online, inbound traffic grows.
- The subscription page opens in a browser on the old link.
- The bot answers; a test payment reaches admin confirmation.

If any item fails and cannot be fixed quickly, roll back (section 5).

## 4. Observation — 1–3 days

- Client traffic is counted, expiry and limits fire (expired users get
  disabled).
- No "can't connect" complaints in the bot or support. Users whose
  Shadowsocks config changed (section 1) got it after a refresh.
- HWID devices register again, nobody hits the limit.
- No repeating errors in master and node logs.
- The master backup arrived in Telegram and restores on the test master.

## 5. Rollback

Remnawave was not changed, so rollback is the cutover in reverse. Time: 5–10
minutes, about one minute of VPN downtime.

1. On every node, stop this panel's node and bring `remnanode` back:

   ```bash
   systemctl stop x-ui && systemctl disable x-ui
   cd /opt/remnanode && docker compose up -d
   ```

2. `cd /opt/remnawave && docker compose up -d`.
3. Point the subscription domain's proxy/DNS back to Remnawave.
4. Stop this panel's bot (clear the token in **Settings → Telegram**) and start
   the old bot.
5. Re-enter in Remnawave by hand everything that changed in the new panel
   after cutover (new users, payments, extensions): take the list from
   **Finance** and the bot log.

Point of no return: the longer the new panel runs, the more manual work step 5
takes. Decide "we stay" within 3 days.

## 6. Wrap-up — after 7 days, 30 minutes

- Remove `remnanode` from nodes: `cd /opt/remnanode && docker compose down -v`,
  delete the directory.
- Remove Remnawave (`docker compose down -v` in `/opt/remnawave`), keeping the
  database dump from step 2.2 archived for another month.
- Revoke the Remnawave API token, restore the normal DNS TTL.
- Mark the move done in [`plan.md`](plan.md), section 3.

## 7. Time summary

| Step | Work | VPN downtime |
| --- | --- | --- |
| 2.1 Inventory | 20–30 min | — |
| 2.2 Backups | 15 min | — |
| 2.3 Master | 30–60 min | — |
| 2.4 Xray template | 30–90 min | — |
| 2.5 Nodes (5) | ~45 min | — |
| 2.6 Dry run | 1–2 h | — |
| 2.7 Subscription settings | 15 min | — |
| 2.8 Bot and payments | 30–60 min | — |
| 2.9 DNS TTL | 2 min, a day ahead | — |
| 3 Cutover | 30–60 min | 1–3 min |
| 4 Observation | 1–3 days, 10–15 min a day | — |
| 5 Rollback (if needed) | 5–10 min + manual re-entry of changes | ~1 min |
| 6 Removing Remnawave | 30 min after 7 days | — |
| **Total** | **~5–8 hours + observation** | **1–3 minutes** |

Steps 2.4 and 2.6 take the most time: the more complex the Remnawave routing,
the longer the template port and the subscription comparison. The rest is
predictable.
