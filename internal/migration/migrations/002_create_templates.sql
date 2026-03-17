-- +goose Up
CREATE TABLE templates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(100) NOT NULL UNIQUE,
    channel    VARCHAR(10)  NOT NULL CHECK (channel IN ('sms','email','push')),
    content    TEXT         NOT NULL,
    variables  JSONB        NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

ALTER TABLE notifications ADD CONSTRAINT fk_notifications_template
    FOREIGN KEY (template_id) REFERENCES templates(id);

-- +goose Down
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS fk_notifications_template;
DROP TABLE IF EXISTS templates;
