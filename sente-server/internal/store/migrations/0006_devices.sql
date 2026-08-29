-- Push registrations (docs/05 §3). One row per device token; a person may have
-- several devices and a device may change hands.
CREATE TABLE devices (
    id           UUID PRIMARY KEY,
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    apns_token   TEXT        NOT NULL,
    bundle_env   TEXT        NOT NULL CHECK (bundle_env IN ('sandbox', 'production')),
    app_version  TEXT,
    push_prefs   JSONB       NOT NULL DEFAULT '{"turn":true,"low_time":true,"game_end":true,"invite":true}'::jsonb,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (apns_token, bundle_env)
);
CREATE INDEX idx_devices_user ON devices (user_id);
