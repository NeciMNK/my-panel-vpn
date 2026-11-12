# Как сливать: ветки, стенд, PR и версии

Короткая инструкция для людей и ИИ-агентов. Общие правила кода, тестов и коммитов — в
[`CLAUDE.md`](CLAUDE.md), состояние проекта — в [`plan.md`](plan.md).

## Ветки

| Ветка | Что в ней | Кто пушит |
| --- | --- | --- |
| `main` | проверенный код; из неё собираются `dev-latest` и версии `vX.Y.Z` | только слияние PR, прямые пуши запрещены |
| `test` | `main` плюс функции, которые сейчас гоняются на тестовом стенде | агент или человек, который проверяет на стенде |
| `<агент>/<кратко>`, `claude/...` | одна задача | автор задачи |

Правило: **всё, что ставится на тестовую ноду или тестовый мастер, собирается из `test`**.
Сборки из веток задач и из `main` на стенд напрямую не ставятся.

## Путь одной задачи

1. **Ветка от свежего `main`.**
   ```bash
   git fetch origin main
   git checkout -b claude/моя-задача origin/main
   ```
2. **Тест → код → `make verify`.** Если задеты база, ноды или синхронизация, ещё
   `make race`, Postgres-тесты и `make node-e2e` (см. `CLAUDE.md`).
3. **PR в `main`.** Описание по-русски по шаблону `.github/pull_request_template.md`:
   что сделано, зачем, что проверено, что нет. Исполнитель — `NeciMNK`. В том же PR
   обновить таблицы раздела 3 в `plan.md`.
4. **Прогон на стенде через `test`** (если задачу нужно проверить на живых серверах):
   ```bash
   git fetch origin main test claude/моя-задача
   git checkout test
   git merge --no-edit origin/main
   git merge --no-edit origin/claude/моя-задача
   git push origin test
   ```
   Сборка из `test`: Actions → «Release 3X-UI» → Run workflow → ветка `test`, архивы
   лежат в артефактах запуска; или локально `make build`. Её ставят на тестовый мастер и
   тестовую ноду. Итог проверки пишется в PR задачи.
5. **Ошибка на стенде** — исправление в ветке задачи (с тестом), затем снова шаг 4.
6. **Всё ок и CI зелёный** — PR сливается в `main` обычным merge-коммитом (не squash и не
   rebase, чтобы сохранилась история).

Ветка `test` — общая. Перед мержем в неё сделайте `fetch`, чтобы не затереть чужие
функции, которые сейчас на проверке. Когда на стенде ничего не гоняется, `test`
выравнивается по `main`:

```bash
git fetch origin main test
git checkout test
git reset --hard origin/main
git push --force-with-lease origin test
```

## Если PR конфликтует с `main`

Обновлять ветку слиянием, а не rebase: `git merge origin/main` в ветке задачи.

- `internal/web/translation/*.json` — оставить ключи обеих сторон.
- Сгенерированные файлы (`frontend/src/generated/`, `frontend/public/openapi.json`,
  `docs/public/openapi.json`, MDX в `docs/content/docs/en/reference/api/`) руками не
  править: взять любую сторону, затем `make gen` и `cd docs && pnpm gen:api`.
- `frontend/src/pages/api-docs/endpoints.ts` — оставить записи обеих сторон.
- Миграции в `internal/database/db.go` — сохранить обе в порядке слияния.

После разрешения — `make verify` и зелёный CI до слияния.

## Подтягивание 3x-ui

Раз в 1–2 недели, отдельным PR `chore(upstream): merge 3x-ui <дата>`:

```bash
git remote add upstream https://github.com/MHSanaei/3x-ui.git   # один раз
git fetch upstream main
git checkout -b claude/upstream-<дата> origin/main
git merge upstream/main
make verify
```

Файл `internal/config/version` при этом остаётся нашим: версия 3x-ui в него не попадает.

## Выпуск версии

Схема номеров (подробно в `README.md`, раздел «Версии»): альфа `v0.0.x`–`v0.4.x`, бета
`v0.5.x`–`v0.9.x`, релиз `v1.0.0` — без кода 3x-ui, под MIT и с новым именем.

1. Все нужные PR слиты в `main`, итоговый `main` проверен на стенде через `test`.
2. PR в `main`, который меняет `internal/config/version` на новый номер (без `v`), и
   строку в разделе 3 `plan.md`. Слить.
3. Тег на этом коммите `main` и пуш тега:
   ```bash
   git fetch origin main
   git tag -a v0.0.2 origin/main -m "v0.0.2 (альфа)"
   git push origin v0.0.2
   ```
4. Workflow «Release 3X-UI» сам собирает архивы и выкладывает их в pre-release с именем
   тега. Затем в описании релиза написать, что вошло (список PR) и что известно как
   недоработка.
5. До `v1.0.0` все версии остаются pre-release, поэтому `install.sh` без тега ставит
   `dev-latest`, а конкретную версию — по тегу: `install.sh v0.0.2`.

Секреты, токены, пароли и внутренние адреса в коммиты, PR, теги и описания релизов не
пишутся — репозиторий публичный.
