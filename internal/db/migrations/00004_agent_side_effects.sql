-- +goose Up

-- Read-only транзакция и ROLLBACK не отменяют всего, что может сделать SELECT:
--   pg_advisory_lock* берёт блокировку уровня сессии — она остаётся на соединении
--   пула после отката, а много таких блокировок исчерпывают общую память
--   кластера и отказывают в подключении всем базам;
--   pg_logical_emit_message(false, …) пишет в WAL нетранзакционно — сотни МБ
--   за секунды, откат их не отменяет.
-- Сервису эти функции не нужны, поэтому они закрыты для всех; снятие
-- блокировок оставлено epinoema_app как страховка после запроса агента.
-- Права на функции pg_catalog меняет только суперпользователь.
-- +goose StatementBegin
DO $$
DECLARE f regprocedure;
BEGIN
  FOR f IN
    SELECT p.oid::regprocedure FROM pg_proc p
    WHERE p.pronamespace = 'pg_catalog'::regnamespace
      AND (p.proname LIKE 'pg\_advisory\_%' OR p.proname LIKE 'pg\_try\_advisory\_%'
           OR p.proname = 'pg_logical_emit_message')
  LOOP
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
  END LOOP;
END $$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION pg_catalog.pg_advisory_unlock_all() TO epinoema_app;

-- REVOKE без прав только предупреждает, и миграция считалась бы применённой.
-- Здесь же проверяется 00003: без суперпользователя она тоже прошла молча.
-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOR f IN
    SELECT p.oid::regprocedure::text FROM pg_proc p
    WHERE p.pronamespace = 'pg_catalog'::regnamespace
      AND (p.proname IN ('set_config', 'pg_logical_emit_message')
           OR p.proname LIKE 'pg\_advisory\_%' OR p.proname LIKE 'pg\_try\_advisory\_%')
      AND has_function_privilege('epinoema_agent_ro', p.oid, 'EXECUTE')
  LOOP
    RAISE EXCEPTION 'epinoema_agent_ro всё ещё может вызвать %: миграции нужно применять суперпользователем', f;
  END LOOP;
END $$;
-- +goose StatementEnd

-- Временные файлы большого запроса агента не должны занять весь диск. Настройка
-- роли входа: SET ROLE её не меняет, поэтому действует и для epinoema_agent_ro.
ALTER ROLE epinoema_app SET temp_file_limit = '512MB';

-- +goose Down

ALTER ROLE epinoema_app RESET temp_file_limit;
REVOKE EXECUTE ON FUNCTION pg_catalog.pg_advisory_unlock_all() FROM epinoema_app;
-- +goose StatementBegin
DO $$
DECLARE f regprocedure;
BEGIN
  FOR f IN
    SELECT p.oid::regprocedure FROM pg_proc p
    WHERE p.pronamespace = 'pg_catalog'::regnamespace
      AND (p.proname LIKE 'pg\_advisory\_%' OR p.proname LIKE 'pg\_try\_advisory\_%'
           OR p.proname = 'pg_logical_emit_message')
  LOOP
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO PUBLIC', f);
  END LOOP;
END $$;
-- +goose StatementEnd
