-- +goose Up
-- Set once someone links their Telegram account by sharing their contact
-- with the club's bot and it matches a phone already on file (see
-- internal/telegrambot) — never settable through the regular API, and
-- never exposed in any response (see users.User.TelegramChatID).
ALTER TABLE users ADD COLUMN telegram_chat_id bigint UNIQUE;

-- +goose Down
ALTER TABLE users DROP COLUMN telegram_chat_id;
