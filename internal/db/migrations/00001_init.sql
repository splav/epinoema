-- +goose Up

-- Роли создаются без LOGIN. LOGIN и пароль для epinoema_app задаются вне миграций.
-- epinoema_agent_ro — роль для произвольных read-only запросов агента;
-- приложение переключается в неё через SET LOCAL ROLE.
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'epinoema_app') THEN
    CREATE ROLE epinoema_app NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'epinoema_agent_ro') THEN
    CREATE ROLE epinoema_agent_ro NOLOGIN;
  END IF;
END $$;
-- +goose StatementEnd
GRANT epinoema_agent_ro TO epinoema_app;

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Текущий пользователь запроса. Приложение выставляет его в начале каждой транзакции:
--   SELECT set_config('app.user_id', $1, true), set_config('app.actor', $2, true);
-- +goose StatementBegin
CREATE FUNCTION app_user_id() RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT nullif(current_setting('app.user_id', true), '')::uuid
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------- пользователи

CREATE TABLE users (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  handle       text NOT NULL UNIQUE,
  display_name text,
  oidc_sub     text UNIQUE,                      -- заполняется на этапе 3
  created_at   timestamptz NOT NULL DEFAULT now()
);

-- Отдельный токен на каждого клиента: так правки агента отличимы от ваших.
CREATE TABLE api_tokens (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  actor      text NOT NULL CHECK (actor IN ('user', 'agent')),
  name       text NOT NULL,                      -- 'cli', 'claude-code', …
  token_hash bytea NOT NULL UNIQUE,              -- sha256 от токена, сам токен не храним
  created_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz
);

-- ---------------------------------------------------------------- заметки

CREATE TABLE notes (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id   uuid NOT NULL REFERENCES users(id),
  title      text,
  body       text NOT NULL DEFAULT '',           -- markdown
  kind       text NOT NULL DEFAULT 'note'
             CHECK (kind IN ('note', 'idea', 'decision', 'meeting', 'project')),
  status     text NOT NULL DEFAULT 'inbox'
             CHECK (status IN ('inbox', 'active', 'parked', 'done', 'dropped')),
  importance smallint CHECK (importance BETWEEN 1 AND 3),
  project    text,
  tags       text[] NOT NULL DEFAULT '{}',
  props      jsonb  NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- Конфиг 'russian' стеммит кириллицу русским snowball, а латиницу английским,
  -- так что смешанный текст ищется нормально.
  search     tsvector GENERATED ALWAYS AS (
               setweight(to_tsvector('russian', coalesce(title, '')), 'A') ||
               setweight(to_tsvector('russian', body), 'B')
             ) STORED
);

CREATE INDEX notes_search_idx     ON notes USING gin (search);
CREATE INDEX notes_title_trgm_idx ON notes USING gin (title gin_trgm_ops);
CREATE INDEX notes_tags_idx       ON notes USING gin (tags);
CREATE INDEX notes_owner_status   ON notes (owner_id, status, updated_at DESC);
CREATE INDEX notes_project_idx    ON notes (project) WHERE project IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION notes_before_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.owner_id <> OLD.owner_id THEN
    RAISE EXCEPTION 'owner_id cannot be changed';
  END IF;
  NEW.updated_at := now();
  RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER notes_before_update
  BEFORE UPDATE ON notes
  FOR EACH ROW EXECUTE FUNCTION notes_before_update();

-- ---------------------------------------------------------------- связи

CREATE TABLE links (
  src_id     uuid NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  dst_id     uuid NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  kind       text NOT NULL DEFAULT 'related'
             CHECK (kind IN ('related', 'derived_from', 'supersedes')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (src_id, dst_id, kind),
  CHECK (src_id <> dst_id)
);

CREATE INDEX links_dst_idx ON links (dst_id);

-- ---------------------------------------------------------------- история правок

-- Каждое изменение заметки сохраняет её предыдущее состояние целиком.
-- Пишется триггером, поэтому ни API, ни агент не могут его обойти.
CREATE TABLE revisions (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  note_id       uuid NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  snapshot      jsonb NOT NULL,
  actor         text NOT NULL CHECK (actor IN ('user', 'agent', 'system')),
  actor_user_id uuid,
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX revisions_note_idx ON revisions (note_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION notes_record_revision() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO revisions (note_id, snapshot, actor, actor_user_id)
  VALUES (
    OLD.id,
    to_jsonb(OLD) - 'search',
    coalesce(nullif(current_setting('app.actor', true), ''), 'system'),
    nullif(current_setting('app.user_id', true), '')::uuid
  );
  RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER notes_record_revision
  AFTER UPDATE ON notes
  FOR EACH ROW EXECUTE FUNCTION notes_record_revision();

-- ---------------------------------------------------------------- доступы и отчёты

-- Пока доступ выдаётся на отдельную заметку; доступ к проекту — следующей миграцией.
CREATE TABLE grants (
  note_id    uuid NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       text NOT NULL CHECK (role IN ('viewer', 'editor')),
  granted_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (note_id, user_id)
);

CREATE INDEX grants_user_idx ON grants (user_id);

-- spec — фильтр в JSON, например:
-- {"kind":["idea"],"status":["active","parked"],"importance_gte":3,"stale_days":14}
CREATE TABLE reports (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       text NOT NULL,
  spec       jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (owner_id, name)
);

-- ---------------------------------------------------------------- row-level security

ALTER TABLE notes     ENABLE ROW LEVEL SECURITY;
ALTER TABLE links     ENABLE ROW LEVEL SECURITY;
ALTER TABLE revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE grants    ENABLE ROW LEVEL SECURITY;
ALTER TABLE reports   ENABLE ROW LEVEL SECURITY;

CREATE POLICY notes_read ON notes FOR SELECT USING (
  owner_id = app_user_id()
  OR EXISTS (SELECT 1 FROM grants g
             WHERE g.note_id = notes.id AND g.user_id = app_user_id())
);
CREATE POLICY notes_insert ON notes FOR INSERT
  WITH CHECK (owner_id = app_user_id());
CREATE POLICY notes_update ON notes FOR UPDATE USING (
  owner_id = app_user_id()
  OR EXISTS (SELECT 1 FROM grants g
             WHERE g.note_id = notes.id AND g.user_id = app_user_id()
               AND g.role = 'editor')
);
CREATE POLICY notes_delete ON notes FOR DELETE
  USING (owner_id = app_user_id());

-- Политики grants не могут напрямую обращаться к notes: политика notes уже читает grants,
-- и Postgres отклонит такую цепочку как бесконечную рекурсию. Проверка владельца
-- вынесена в SECURITY DEFINER функцию, которая читает notes в обход RLS.
-- +goose StatementBegin
CREATE FUNCTION note_owned_by_me(p_note uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM notes WHERE id = p_note AND owner_id = app_user_id())
$$;
-- +goose StatementEnd

CREATE POLICY grants_read ON grants FOR SELECT
  USING (user_id = app_user_id() OR granted_by = app_user_id());
CREATE POLICY grants_manage ON grants FOR ALL
  USING (granted_by = app_user_id())
  WITH CHECK (granted_by = app_user_id() AND note_owned_by_me(note_id));

-- Подзапросы к notes сами проходят через RLS, поэтому «заметка существует» = «заметка видна».
CREATE POLICY links_read ON links FOR SELECT USING (
  EXISTS (SELECT 1 FROM notes n WHERE n.id = links.src_id)
  AND EXISTS (SELECT 1 FROM notes n WHERE n.id = links.dst_id)
);
CREATE POLICY links_insert ON links FOR INSERT WITH CHECK (
  EXISTS (SELECT 1 FROM notes n WHERE n.id = links.src_id)
  AND EXISTS (SELECT 1 FROM notes n WHERE n.id = links.dst_id)
);
CREATE POLICY links_delete ON links FOR DELETE USING (
  EXISTS (SELECT 1 FROM notes n WHERE n.id = links.src_id)
);

CREATE POLICY revisions_read ON revisions FOR SELECT
  USING (EXISTS (SELECT 1 FROM notes n WHERE n.id = revisions.note_id));

CREATE POLICY reports_own ON reports FOR ALL
  USING (owner_id = app_user_id())
  WITH CHECK (owner_id = app_user_id());

-- ---------------------------------------------------------------- права ролей

GRANT SELECT, INSERT, UPDATE, DELETE ON notes, links, grants, reports TO epinoema_app;
GRANT SELECT, INSERT, UPDATE          ON users, api_tokens            TO epinoema_app;
GRANT SELECT                          ON revisions                    TO epinoema_app;

-- Агенту — только чтение и только содержимого: ни токенов, ни пользователей.
-- grants нужен потому, что его читает политика notes (строки всё равно режет RLS).
GRANT SELECT ON notes, links, reports, revisions, grants TO epinoema_agent_ro;


-- +goose Down

DROP TABLE IF EXISTS reports, grants, revisions, links, notes, api_tokens, users CASCADE;
DROP FUNCTION IF EXISTS note_owned_by_me(uuid), notes_record_revision(), notes_before_update(), app_user_id();
-- Роли epinoema_app и epinoema_agent_ro общие для кластера, удаляются вручную.
