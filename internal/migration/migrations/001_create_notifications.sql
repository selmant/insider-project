-- +goose Up
CREATE TABLE notifications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id        UUID,
    idempotency_key VARCHAR(255) UNIQUE,
    recipient       VARCHAR(320) NOT NULL,
    channel         VARCHAR(10)  NOT NULL CHECK (channel IN ('sms','email','push')),
    content         TEXT         NOT NULL,
    priority        VARCHAR(10)  NOT NULL DEFAULT 'normal' CHECK (priority IN ('high','normal','low')),
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','scheduled','queued','processing','sent','failed','cancelled')),
    template_id     UUID,
    template_vars   JSONB,
    scheduled_at    TIMESTAMPTZ,
    provider_msg_id VARCHAR(255),
    attempts        INT NOT NULL DEFAULT 0,
    max_attempts    INT NOT NULL DEFAULT 5,
    last_error      TEXT,
    sent_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_status ON notifications(status);
CREATE INDEX idx_notifications_batch_id ON notifications(batch_id);
CREATE INDEX idx_notifications_channel_status ON notifications(channel, status);
CREATE INDEX idx_notifications_scheduled_at ON notifications(scheduled_at) WHERE scheduled_at IS NOT NULL;
CREATE INDEX idx_notifications_created_at ON notifications(created_at);

-- +goose Down
DROP TABLE IF EXISTS notifications;
