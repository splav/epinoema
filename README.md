# Epinoema

Self-hosted база идей и заметок. Один бинарник `epi`: сервер API, админка и CLI.
Статус: ранний MVP, этапы 0–2 (захват, агент, поиск и отчёты). Лицензия MIT.

```sh
go install github.com/splav/epinoema/cmd/epi@latest
```

## Установка на macOS для ежедневной работы

```sh
./scripts/setup-macos.sh
```

Скрипт ставит `epi` в `~/go/bin`, применяет миграции, выпускает токены человека и агента
в `~/.config/epi/` (права 600), запускает `epi serve` как launchd-агент (поднимается при входе,
перезапускается при падении, лог в `~/Library/Logs/epinoema.log`) и подключает MCP-сервер
и skill к Claude Code. Повторный запуск обновляет бинарник и сервис, токены не трогает.
Нужны Go, PostgreSQL 16 из brew и файлы словаря (ниже).

Остановить сервис: `launchctl bootout gui/$(id -u)/dev.epinoema.serve`.

## Локальный запуск вручную

Нужны Go 1.24+ и PostgreSQL 16+ (`brew install go postgresql@16`).

Поиск использует русский словарь hunspell. Его два файла нужно положить в каталог
словарей Postgres, иначе миграция остановится с ошибкой:

```sh
git clone --depth 1 https://github.com/postgrespro/hunspell_dicts /tmp/hunspell_dicts
cp /tmp/hunspell_dicts/hunspell_ru_ru/ru_ru.{dict,affix} "$(pg_config --sharedir)/tsearch_data/"
```

Словарь — Alexander I. Lebedev, лицензия BSD-3 (`hunspell_ru_ru/license` в том же репозитории).

```sh
go build -o bin/epi ./cmd/epi

createdb epinoema
EPI_ADMIN_DB=postgres://localhost/epinoema bin/epi migrate

# Роли создаются миграцией без LOGIN. Сервис ходит под epinoema_app,
# а не под владельцем таблиц: владелец обходит RLS.
psql -d epinoema -c "ALTER ROLE epinoema_app LOGIN"   # на сервере ещё PASSWORD '…'

export EPI_DB=postgres://epinoema_app@localhost/epinoema
bin/epi token create -user $USER -name cli    # токен показывается один раз
bin/epi serve
```

В другом терминале:

```sh
export EPI_TOKEN=epi_…            # EPI_URL по умолчанию http://127.0.0.1:7777
bin/epi add "идея"
echo "длинная мысль" | bin/epi add -t "заголовок" -tag a,b -p проект -i 2
bin/epi inbox
bin/epi report review          # еженедельное ревью: входящие, забытые важные идеи, неделя
bin/epi report all -p проект   # всё по проекту
bin/epi project                # проекты и число заметок
bin/epi project rename старое новое   # переименовать или слить с существующим
```

Отчёт — JSON-фильтр. Встроенные: `review`, `inbox`, `stale`, `week`, `all`. Свой:

```sh
echo '{"kind":["decision"],"changed_days":30}' | bin/epi report save decisions
bin/epi report decisions
```

Поля фильтра: `kind`, `status`, `project`, `tags_any`, `importance_gte`, `stale_days`,
`changed_days`, `order` (`updated`, `created`, `importance`), `limit`. Несколько секций:
`{"sections":[{"title":"…","filter":{…}}]}`.

## Агент (MCP)

`epi mcp` — stdio MCP-сервер поверх того же API. Агенту нужен свой токен: так в истории
правок видно, что менял агент, а `epi mcp` с токеном человека не запустится.

```sh
AGENT_TOKEN=$(bin/epi token create -user $USER -actor agent -name claude-code)
claude mcp add epinoema --scope user \
  --env EPI_TOKEN=$AGENT_TOKEN --env EPI_URL=http://127.0.0.1:7777 \
  -- "$PWD/bin/epi" mcp
```

Инструменты: `add_note`, `list_inbox`, `search`, `get_note`, `update_meta`, `link`,
`list_reports`, `run_report`, `save_report`, `query`, `list_projects`, `rename_project`. `update_meta` меняет только метаданные,
текст заметки агент изменить не может. `query` — произвольный SELECT для разовых отчётов:
под ролью `epinoema_agent_ro`, только чтение, с таймаутом 5 с; строки режет RLS, токены и
пользователи недоступны, подменить пользователя нельзя (`TestQueryCannotEscape`).

Правила записи и разбора входящих — в [`skills/epinoema-inbox/SKILL.md`](skills/epinoema-inbox/SKILL.md).
Краткую версию MCP-сервер сам отдаёт клиенту при подключении. Полную подключите в Claude Code:

```sh
ln -s "$PWD/skills/epinoema-inbox" ~/.claude/skills/epinoema-inbox
```

После этого «разбери входящие» в Claude Code покажет таблицу предложений и применит
их после подтверждения.

## Разработка

```sh
sqlc generate      # internal/db/queries/*.sql → internal/db/dbq
EPI_TEST_ADMIN_DB=postgres://localhost/postgres go test ./...
```

Миграции применяет суперпользователь: `00003` и `00004` отзывают у `PUBLIC` функции,
через которые агент мог бы выдать себя за другого пользователя или оставить последствия
после отката (`set_config`, advisory-блокировки, `pg_logical_emit_message`), а права на функции
`pg_catalog` меняет только он. Без суперпользователя миграция `00004` остановится с ошибкой,
а `epi serve` не запустится. Список volatile-функций, доступных агенту, закреплён в
`internal/db/testdata` (`TestAgentCallableFunctions`).

Тесты создают временную базу на каждый тест и удаляют её. Без `EPI_TEST_ADMIN_DB`
интеграционные тесты пропускаются.

| Путь | Что там |
| --- | --- |
| `cmd/epi` | подкоманды: `migrate`, `serve`, `token create`, `add`, `inbox`, `mcp` |
| `internal/db/migrations` | goose-миграции, встроены в бинарник |
| `internal/db/queries` | SQL для sqlc; `dbq` — сгенерированный код, руками не править |
| `internal/db` | пул, миграции, `InTx` — транзакция от имени пользователя |
| `internal/server` | HTTP API и авторизация по токену |
| `internal/api` | JSON-контракт API для сервера и клиентов |
| `internal/client` | HTTP-клиент для CLI и MCP |
| `internal/mcpserver` | MCP-сервер: инструменты агента поверх клиента |
| `internal/testenv` | временная база и сервер для интеграционных тестов |
| `skills/epinoema-inbox` | правила для агента |

## Известные ограничения

- При удалении заметки её история удаляется вместе с ней.
- Поиск находит формы одного слова, но не слова с разными корнями («человек» и «люди»)
  и не сленг, которого нет в словаре («деплой» и «деплоя»).

## До этапа 3 (шаринг)

Найдено на ревью безопасности; пока пользователь один, не проявляется.

- Viewer может создавать и удалять связи между расшаренными ему заметками: политики `links`
  требуют только видимости. Нужно «владелец или editor» для `src`.
- Viewer видит всю историю правок заметки, включая текст до шаринга. Ревизии — только владельцу.
- Списки, поиск и отчёты показывают всё видимое, в том числе чужие расшаренные заметки:
  они попадут во входящие и в ревью получателя. По умолчанию — только свои.
- Агент видит статистику таблиц (`pg_stat_user_tables`, `pg_class.reltuples`) и по ней —
  активность других пользователей.
- Нет команды отзыва токена и срока жизни токенов. Ограничения агента (не менять текст)
  держатся на API без такого эндпоинта, а не на проверке actor на сервере.
