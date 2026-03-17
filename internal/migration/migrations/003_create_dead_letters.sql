-- +goose Up
CREATE TABLE dead_letters (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    notification_id UUID NOT NULL REFERENCES notifications(id),
    last_error      TEXT NOT NULL,
    attempts        INT  NOT NULL,
    failed_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_dead_letters_notification_id ON dead_letters(notification_id);

-- +goose Down
DROP TABLE IF EXISTS dead_letters;
