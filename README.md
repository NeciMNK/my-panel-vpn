<div align="center">

# 🛰️ my-panel-vpn

**Одна панель для всех VPN-серверов: мастер управляет нодами, пользователь получает одну подписку на всё.**

[![Версия](https://img.shields.io/github/v/release/NeciMNK/my-panel-vpn?include_prereleases&label=%D0%B2%D0%B5%D1%80%D1%81%D0%B8%D1%8F&color=orange)](https://github.com/NeciMNK/my-panel-vpn/releases)
[![Стадия](https://img.shields.io/badge/%D1%81%D1%82%D0%B0%D0%B4%D0%B8%D1%8F-%D0%B0%D0%BB%D1%8C%D1%84%D0%B0-orange)](#-версии)
[![CI](https://img.shields.io/github/actions/workflow/status/NeciMNK/my-panel-vpn/ci.yml?branch=main&label=CI)](https://github.com/NeciMNK/my-panel-vpn/actions/workflows/ci.yml)
[![Лицензия](https://img.shields.io/badge/%D0%BB%D0%B8%D1%86%D0%B5%D0%BD%D0%B7%D0%B8%D1%8F-GPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](frontend/package.json)

[Установка](#-быстрая-установка) · [Возможности](#-возможности) · [Версии](#-версии) · [Разработка](#-разработка) · [План](plan.md) · [Как сливать](MERGING.ru.md)

</div>

> [!NOTE]
> **my-panel-vpn** — личный форк [3X-UI](https://github.com/MHSanaei/3x-ui), не официальный релиз 3X-UI.
> Лицензия GPL-3.0, как у апстрима; авторство, список изменений и сторонние лицензии — в [`NOTICE.md`](NOTICE.md).

От [3x-ui v3](https://github.com/MHSanaei/3x-ui) взяты ядро, интерфейс и все протоколы Xray.
От [Remnawave](https://github.com/remnawave/panel) взяты идеи: нода одной командой, конфиг нод
с мастера, страница подписки с инструкцией по установке приложений. Код Remnawave
(TypeScript, AGPL-3.0) в проект не входит.

> [!IMPORTANT]
> Проект для личного использования, не продукт и не сервис. Используйте только в рамках закона.

## 🚀 Быстрая установка

На чистом сервере (Ubuntu, Debian и другие Linux), от root:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/NeciMNK/my-panel-vpn/main/install.sh)
```

Установщик скачивает готовую сборку форка, ставит Xray, systemd-сервис и меню `x-ui`, а в
конце показывает адрес панели, логин и пароль. Ноды добавляются с мастера одной командой
(см. [«Ноды»](#ноды)).

## ✨ Возможности

<table>
<tr>
<td width="50%" valign="top">

### 🧭 Мастер и ноды

- **Мастер + ноды.** Мастер хранит пользователей, inbound'ы и настройки Xray и раскатывает
  их на ноды по HTTPS с mTLS. Трафик, онлайн и состояние нод собираются на мастере.
- **Нода одной командой.** «Ноды» → «Добавить ноду» выдаёт одноразовую команду: скрипт сам
  ставит сборку, выпускает сертификат и регистрируется.
- **Xray нод с мастера.** Outbound'ы, маршрутизация, балансировщики, DNS и WARP любой
  ноды редактируются прямо на мастере.
- **Несколько мастеров.** Patroni + keepalived, автоматическое переключение и возврат
  главенства; порядок мастеров меняется кнопками в панели.
- **PostgreSQL** как основная база (SQLite остаётся для простого случая).

</td>
<td width="50%" valign="top">

### 🔀 Каскады и балансировка

- **Каскад в 2 клика.** Вход → выход: на выходах поднимается скрытый VLESS+REALITY, в
  подписке пользователя остаётся только вход. Режимы: разделение трафика на входе,
  чистый релей или оба сразу.
- **Балансировщик по стране.** «Все серверы DE» как выход: новая нода страны попадает в
  балансировщик сама, трафик идёт через самый быстрый сервер. Есть запасная страна.
- **Липкие сессии и закрепление.** Выход (и внешний IP) держится 15 минут после обрыва;
  пользователя можно закрепить за конкретным выходом навсегда.
- **Скорость.** Максимум режется через `tc`, минимум работает как порог качества. Скорость
  канала каждого сервера панель измеряет сама.

</td>
</tr>
<tr>
<td valign="top">

### 🏠 Сети

- **WARP одним переключателем** для каждого сервера: выключен, весь трафик или только
  выбранные сайты.
- **Домашняя сеть.** Доступ к домашней локалке через сервер-шлюз (WireGuard, OpenVPN,
  L2TP/IPsec или IKEv2). Правила «пользователь/группа → вся сеть, подсеть или IP»,
  остальным домашние адреса закрыты.
- **ИИ-сервисы с постоянного IP.** Доверенный клиент закрепляет в боте выход для ИИ, в том
  числе с выбором страны («только NL»).

</td>
<td valign="top">

### 👥 Пользователи и деньги

- **Страница подписки с инструкцией.** В браузере ссылка определяет устройство и помогает
  поставить приложение; VPN-клиенты по ней же получают обычную подписку.
- **Telegram-бот с оплатой.** Дата, сумма и реквизиты; «Я оплатил» и подтверждение одним
  нажатием, оплата картой через ЮKassa, несколько месяцев со скидкой, отсрочка, скидка друзьям.
- **Финансы.** Расходы на серверы (с привязкой к нодам и их IP), доходы, итог месяца,
  напоминания об оплате серверов.

</td>
</tr>
<tr>
<td valign="top">

### 📦 Переезд

- **С Remnawave и 3x-ui без перевыпуска конфигов.** Отдельная вкладка настроек «Перенести
  из»: UUID, пароли, ключи REALITY, subId, лимиты и трафик сохраняются, старый путь
  подписки можно оставить. План по шагам: [`migration-plan.ru.md`](migration-plan.ru.md).
- **Установщик заменяет старую 3x-ui** с резервной копией базы и не трогает Blitz.

</td>
<td valign="top">

### 🔒 SSL

- **Let's Encrypt через Cloudflare DNS**, включая wildcard, без порта 80. Автопродление за
  30 дней до конца срока и раздача сертификатов на все ноды; в форме TLS inbound'а есть
  выбор выпущенного сертификата.

</td>
</tr>
</table>

<details>
<summary><b>Всё, что уже было в 3x-ui v3</b></summary>

- Все протоколы Xray: VLESS (REALITY, XTLS Vision), VMess, Trojan, Shadowsocks 2022,
  WireGuard, Hysteria2, TUIC v5, AmneziaWG, MTProto, HTTP/SOCKS, Tunnel, TUN.
- Транспорты TCP, mKCP, WebSocket, gRPC, HTTPUpgrade, XHTTP; fallback'и на одном порту.
- Лимиты трафика, сроки, лимит IP и устройств (HWID), группы клиентов, хосты для подписок.
- Подписки в форматах raw, JSON и Clash; REST API с токенами; 14 языков интерфейса.

</details>

## 🖼️ Скриншоты

<details>
<summary>Показать</summary>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./media/01-overview-dark.png">
  <img alt="Обзор" src="./media/01-overview-light.png">
</picture>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./media/02-add-inbound-dark.png">
  <img alt="Inbound'ы" src="./media/02-add-inbound-light.png">
</picture>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./media/03-add-client-dark.png">
  <img alt="Добавление клиента" src="./media/03-add-client-light.png">
</picture>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="./media/05-add-nodes-dark.png">
  <img alt="Ноды" src="./media/05-add-nodes-light.png">
</picture>

</details>

## 🏷️ Версии

| Стадия | Номера | Когда | Лицензия и имя |
| --- | --- | --- | --- |
| 🧪 **Альфа** (сейчас) | `v0.0.x` … `v0.4.x` | форк 3x-ui, функции добавляются и проверяются на тестовом стенде | GPL-3.0, my-panel-vpn |
| 🧩 **Бета** | `v0.5.x` … `v0.9.x` | пройдена половина перехода на собственный код и идёт тест багов перед релизом | GPL-3.0, пока в сборке есть код 3x-ui |
| 🚀 **Релиз** | `v1.0.0` | в панели не осталось кода 3x-ui | **MIT** и новое имя |

Все версии до `v1.0.0` публикуются на GitHub как pre-release. Кроме них после каждого
изменения в `main` CI выкладывает сборку `dev-latest`. Как именно уходим от кода 3x-ui и
почему MIT возможна только после полной замены, написано в [`plan.md`](plan.md), разделы 7 и 8.

### 💡 Как назвать панель после ухода от 3x-ui

Имя без «x-ui», «3x» и «remna», чтобы не путать с чужими проектами. Варианты:

| Имя | Почему |
| --- | --- |
| **Orbita** ⭐ | мастер в центре, ноды на орбите вокруг него; коротко и читается на любом языке |
| **Kaskad** | каскады в два клика — главная фишка панели |
| **Shlyuz** | шлюз: и в интернет, и в домашнюю сеть |
| **Nodara** | от «node»: панель про ноды, звучит как имя собственное |
| **Veilnet** | «сеть под вуалью», намёк на REALITY и скрытые связки |
| **Mostik** | мостик между пользователем и серверами, по-домашнему |

Перед выбором стоит проверить, что имя свободно на GitHub, в доменах и среди товарных знаков.

## 📥 Установка

### Мастер

Команда из [«Быстрой установки»](#-быстрая-установка) ставит последнюю стабильную версию,
а пока её нет, сборку `dev-latest` из `main`. Конкретную версию можно указать тегом:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/NeciMNK/my-panel-vpn/main/install.sh) <тег>
```

Во время установки можно выбрать PostgreSQL: установщик поставит его сам или примет строку
подключения к существующему серверу. Для установки без вопросов (cloud-init) задайте
`XUI_NONINTERACTIVE=1`: логин и пароль будут записаны в `/etc/x-ui/install-result.env`.

Если на сервере уже стоит оригинальная 3x-ui (или другая сборка x-ui), установщик спросит
подтверждение, остановит её, сохранит базу, настройки и юнит в `/root/3x-ui-backup-<дата>/`
и поставит форк начисто. Клиентов из старой панели можно перенести, загрузив
`/root/3x-ui-backup-<дата>/x-ui/x-ui.db` в «Перенести из» (источник 3x-ui). Панель Blitz
(Hysteria2, `/etc/hysteria`) установщик не трогает.

Обновление: `x-ui update` в консоли или кнопка обновления в панели. Оба берут сборку этого
форка, а не оригинального 3x-ui.

### Ноды

На мастере: «Ноды» → «Добавить ноду» → скопировать команду и выполнить её на новом сервере:

```bash
curl -fsSL https://мастер/путь/node-join/install.sh | bash -s -- \
  --master https://мастер/путь --join <токен> --country DE
```

Нода скачивает сборку прямо с мастера, поэтому версии мастера и нод всегда совпадают.

### База данных

Мастер рассчитан на PostgreSQL. Если базу создал установщик, он сам прописывает её в
`/etc/default/x-ui`:

```
XUI_DB_TYPE=postgres
XUI_DB_DSN=postgres://xui:пароль@127.0.0.1:5432/xui?sslmode=disable
```

Перенос существующей установки с SQLite:

```bash
x-ui migrate-db --dsn "postgres://xui:пароль@127.0.0.1:5432/xui?sslmode=disable"
# затем прописать XUI_DB_TYPE и XUI_DB_DSN в /etc/default/x-ui
systemctl restart x-ui
```

Файл SQLite при переносе не трогается, удалите его сами после проверки.

### Сборка из исходников

Нужны Go 1.27+, Node.js 26+ с npm 11+ и компилятор C (для SQLite):

```bash
git clone https://github.com/NeciMNK/my-panel-vpn.git
cd my-panel-vpn
cd frontend && npm ci && npm run build && cd ..
go build -o x-ui .
```

## 🛠️ Разработка

| Документ | О чём |
| --- | --- |
| [`plan.md`](plan.md) | состояние проекта, дорожная карта, путь к своей панели |
| [`MERGING.ru.md`](MERGING.ru.md) | ветки `main` и `test`, как проверять на стенде и сливать PR, как выпускать версии |
| [`CLAUDE.md`](CLAUDE.md), [`CONTRIBUTING.md`](CONTRIBUTING.md) | обязательные правила изменений и проверки |
| [`docs/architecture.md`](docs/architecture.md) | карта кода |

Полная локальная проверка перед PR: `make verify`, все цели: `make help`.

Исправления из 3x-ui подтягиваются обычным слиянием (`git merge upstream/main`, подробно в
[`MERGING.ru.md`](MERGING.ru.md)). Всё, что форк дописывает в шаблоны Xray, помечено префиксом
`mp-<функция>-<id>-`, поэтому каждая функция переписывает только свои outbound'ы, правила и
балансировщики и не трогает ручные настройки.

## 📜 Лицензия и благодарности

Проект распространяется под [GPL-3.0](LICENSE), как и 3x-ui. Авторство и сторонние лицензии —
в [`NOTICE.md`](NOTICE.md). Переход на MIT запланирован только для релиза `v1.0.0`, в котором
не останется кода 3x-ui.

- [3x-ui](https://github.com/MHSanaei/3x-ui) и [alireza0](https://github.com/alireza0/) — основа панели.
- [Remnawave](https://github.com/remnawave/panel) — идеи управления нодами и страницы подписки.
- [Xray-core](https://github.com/XTLS/Xray-core) — ядро.
- [Russia v2ray rules](https://github.com/runetfreedom/russia-v2ray-rules-dat) и
  [Iran v2ray rules](https://github.com/chocolate4u/Iran-v2ray-rules) (GPL-3.0) — геоданные
  для маршрутизации.
