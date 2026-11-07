#!/bin/sh
# Ставит Epinoema для ежедневной работы на macOS. Повторный запуск безопасен:
# обновляет бинарник и сервис, токены и настройки не перевыпускает.
#
#   ./scripts/setup-macos.sh
#
# Нужны Go, PostgreSQL 16 с текущим пользователем-суперпользователем (так в brew)
# и файлы словаря hunspell — см. README.
set -eu

REPO=$(cd "$(dirname "$0")/.." && pwd)
DB_NAME=${EPI_DB_NAME:-epinoema}
ADDR=${EPI_ADDR:-127.0.0.1:7777}
APP_DSN="postgres://epinoema_app@localhost:5432/$DB_NAME"
CONF_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/epi"
LABEL=dev.epinoema.serve
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG="$HOME/Library/Logs/epinoema.log"

step() { printf '\n==> %s\n' "$*"; }

# postgresql@16 в brew не попадает в PATH сам.
PG_PREFIX=$(brew --prefix postgresql@16 2>/dev/null || true)
[ -n "$PG_PREFIX" ] && [ -d "$PG_PREFIX/bin" ] && PATH="$PG_PREFIX/bin:$PATH"

step "Сборка epi"
(cd "$REPO" && go install ./cmd/epi)
EPI="$(go env GOPATH)/bin/epi"
echo "$EPI"

step "База $DB_NAME и миграции"
createdb "$DB_NAME" 2>/dev/null && echo "создана" || echo "уже есть"
EPI_ADMIN_DB="postgres://localhost:5432/$DB_NAME" "$EPI" migrate
# Роли создаются миграцией без LOGIN. Локально пароль не нужен: brew-Postgres
# доверяет подключениям с localhost.
psql -q -d "$DB_NAME" -c "ALTER ROLE epinoema_app LOGIN"

step "Токены и $CONF_DIR/env"
mkdir -p "$CONF_DIR" && chmod 700 "$CONF_DIR"
if [ -f "$CONF_DIR/env" ]; then
  echo "уже есть, не трогаю"
else
  CLI_TOKEN=$(EPI_DB="$APP_DSN" "$EPI" token create -user "$USER" -name cli)
  umask 077
  cat > "$CONF_DIR/env" <<CONF
# Настройки epi. Переменные окружения важнее этого файла.
EPI_URL=http://$ADDR
EPI_TOKEN=$CLI_TOKEN
EPI_DB=$APP_DSN
CONF
  EPI_DB="$APP_DSN" "$EPI" token create -user "$USER" -actor agent -name claude-code > "$CONF_DIR/agent-token"
  echo "записаны env и agent-token (права 600)"
fi

step "Сервис launchd ($LABEL)"
mkdir -p "$HOME/Library/LaunchAgents" "$(dirname "$LOG")"
cat > "$PLIST" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array><string>$EPI</string><string>serve</string><string>-addr</string><string>$ADDR</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>HOME</key><string>$HOME</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>$LOG</string>
  <key>StandardErrorPath</key><string>$LOG</string>
</dict>
</plist>
PL
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  curl -fsS "http://$ADDR/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "http://$ADDR/healthz" >/dev/null || { echo "сервис не ответил, лог: $LOG"; exit 1; }
echo "отвечает на http://$ADDR, лог: $LOG"

step "Claude Code"
if command -v claude >/dev/null 2>&1; then
  if claude mcp get epinoema >/dev/null 2>&1; then
    echo "MCP-сервер epinoema уже подключён"
  else
    claude mcp add epinoema --scope user \
      --env EPI_TOKEN="$(cat "$CONF_DIR/agent-token")" --env EPI_URL="http://$ADDR" \
      -- "$EPI" mcp
  fi
else
  echo "claude не найден — подключите MCP вручную, см. README"
fi
SKILL="$HOME/.claude/skills/epinoema-inbox"
if [ -e "$SKILL" ]; then
  echo "skill уже на месте: $SKILL"
else
  mkdir -p "$HOME/.claude/skills" && ln -s "$REPO/skills/epinoema-inbox" "$SKILL"
  echo "skill подключён: $SKILL"
fi

step "Готово"
echo "Проверка: epi add \"первая идея\" && epi inbox"
case ":$PATH:" in *":$(dirname "$EPI"):"*) ;; *) echo "Добавьте $(dirname "$EPI") в PATH, чтобы вызывать epi без пути." ;; esac
