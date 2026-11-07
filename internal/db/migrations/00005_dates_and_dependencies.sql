-- +goose Up

-- Сроки для диаграммы Ганта. Обе даты необязательны: задача без start_on
-- встаёт после своих зависимостей, без due_on длится один день.
ALTER TABLE notes
  ADD COLUMN start_on date,
  ADD COLUMN due_on   date,
  ADD CONSTRAINT notes_dates_order CHECK (due_on >= start_on);

-- depends_on: src начинается после окончания dst.
ALTER TABLE links DROP CONSTRAINT links_kind_check;
ALTER TABLE links ADD CONSTRAINT links_kind_check
  CHECK (kind IN ('related', 'derived_from', 'supersedes', 'depends_on'));

-- +goose Down

DELETE FROM links WHERE kind = 'depends_on';
ALTER TABLE links DROP CONSTRAINT links_kind_check;
ALTER TABLE links ADD CONSTRAINT links_kind_check
  CHECK (kind IN ('related', 'derived_from', 'supersedes'));
ALTER TABLE notes DROP CONSTRAINT notes_dates_order, DROP COLUMN start_on, DROP COLUMN due_on;
