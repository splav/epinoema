-- +goose Up

-- Произвольные запросы агента идут под ролью epinoema_agent_ro, а пользователя
-- и роль запроса определяют настройки app.user_id и role. По умолчанию set_config
-- может вызвать кто угодно, и одним SELECT агент мог выдать себя за другого
-- пользователя или вернуться в роль epinoema_app:
--   SELECT set_config('app.user_id', '<чужой id>', true), (SELECT body FROM notes)
--   SELECT set_config('role', 'epinoema_app', true), …
-- Право остаётся только у роли сервиса. Агент её прав не наследует: членство
-- выдано в обратную сторону (epinoema_app входит в epinoema_agent_ro).
--
-- Команду SET агент выполнить не может: сервис оборачивает его запрос
-- в подзапрос, где допустим только SELECT. query_to_xml и подобные функции
-- выполняют строку как read-only SPI, где SET запрещён, а set_config теперь недоступен.
--
-- Права на функции pg_catalog меняет только суперпользователь.
REVOKE EXECUTE ON FUNCTION pg_catalog.set_config(text, text, boolean) FROM PUBLIC;
GRANT  EXECUTE ON FUNCTION pg_catalog.set_config(text, text, boolean) TO epinoema_app;

-- +goose Down

GRANT  EXECUTE ON FUNCTION pg_catalog.set_config(text, text, boolean) TO PUBLIC;
REVOKE EXECUTE ON FUNCTION pg_catalog.set_config(text, text, boolean) FROM epinoema_app;
