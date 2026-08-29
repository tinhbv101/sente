-- Rotating refresh tokens (docs/03 ADR-008, docs/08 §2.2). Only a hash is stored:
-- a leaked table yields nothing usable.

CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BYTEA       NOT NULL UNIQUE,
    -- Every rotation stays in the same family. Presenting an already-rotated token
    -- is the signature of a stolen copy, and revokes the whole family.
    family_id   UUID        NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_family ON refresh_tokens (family_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_refresh_expiry ON refresh_tokens (expires_at) WHERE revoked_at IS NULL;
