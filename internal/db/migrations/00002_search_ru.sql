-- +goose Up

-- Поиск с русской морфологией. Snowball режет слова по окончаниям и путается
-- в формах одного слова: «идея» → «иде», а «идеи» → «ид». Словарь hunspell приводит
-- слово к начальной форме, а неизвестные ему слова по-прежнему стеммит snowball.
--
-- Нужны файлы ru_ru.dict и ru_ru.affix в $(pg_config --sharedir)/tsearch_data,
-- без них миграция упадёт здесь же. Взять их можно в
-- https://github.com/postgrespro/hunspell_dicts (hunspell_ru_ru, лицензия BSD-3).
-- Словарь создаётся напрямую, без CREATE EXTENSION hunspell_ru_ru: расширение
-- требует суперпользователя, а словарю хватает права CREATE на схему.
CREATE TEXT SEARCH DICTIONARY epi_ru_hunspell (
  TEMPLATE  = ispell,
  DictFile  = ru_ru,
  AffFile   = ru_ru,
  StopWords = russian
);

-- unaccent стоит первым и сводит «ё» к «е»: иначе hunspell не сопоставляет
-- «ребёнок» и «ребенка», хотя snowball их сопоставлял.
CREATE EXTENSION IF NOT EXISTS unaccent;

CREATE TEXT SEARCH CONFIGURATION epi_ru (COPY = russian);
ALTER TEXT SEARCH CONFIGURATION epi_ru
  ALTER MAPPING FOR word, hword, hword_part
  WITH unaccent, epi_ru_hunspell, russian_stem;

-- В PostgreSQL 16 выражение генерируемой колонки не меняется, поэтому колонка
-- пересоздаётся; индекс удаляется вместе с ней.
ALTER TABLE notes DROP COLUMN search;
ALTER TABLE notes ADD COLUMN search tsvector GENERATED ALWAYS AS (
  setweight(to_tsvector('epi_ru', coalesce(title, '')), 'A') ||
  setweight(to_tsvector('epi_ru', body), 'B')
) STORED;
CREATE INDEX notes_search_idx ON notes USING gin (search);

-- +goose Down

ALTER TABLE notes DROP COLUMN search;
ALTER TABLE notes ADD COLUMN search tsvector GENERATED ALWAYS AS (
  setweight(to_tsvector('russian', coalesce(title, '')), 'A') ||
  setweight(to_tsvector('russian', body), 'B')
) STORED;
CREATE INDEX notes_search_idx ON notes USING gin (search);
DROP TEXT SEARCH CONFIGURATION epi_ru;
DROP TEXT SEARCH DICTIONARY epi_ru_hunspell;
-- unaccent не удаляется: расширение могло понадобиться не только нам.
