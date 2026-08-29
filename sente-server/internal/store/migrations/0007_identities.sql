-- Sign-in providers attached to an account (docs/05 §3). A guest becomes a real
-- account by gaining a row here; the games stay with the same user id.
CREATE TABLE user_identities (
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider         TEXT        NOT NULL CHECK (provider IN ('apple')),
    provider_subject TEXT        NOT NULL,
    email            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_subject),
    UNIQUE (user_id, provider)
);
