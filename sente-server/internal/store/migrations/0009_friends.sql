-- Friends (docs/01 FR-A3, docs/12 C1). One row per pair, stored in canonical
-- order, so "are these two connected?" is a primary-key lookup and two people
-- asking each other at the same instant collide on the key instead of racing.
--
-- 'declined' is kept rather than deleted: the tombstone is what stops a refused
-- request being sent again and again. Only the other person asking can revive it.

CREATE TABLE friendships (
    user_low     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_high    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Who asked. On a declined row it is who was refused, which is what makes
    -- the refusal stick to that direction only.
    requester_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       TEXT        NOT NULL CHECK (status IN ('pending', 'accepted', 'declined')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at   TIMESTAMPTZ,

    PRIMARY KEY (user_low, user_high),
    CONSTRAINT friendship_order CHECK (user_low < user_high),
    CONSTRAINT requester_is_member CHECK (requester_id IN (user_low, user_high))
);

-- The primary key already indexes user_low; the other half needs its own.
CREATE INDEX idx_friendships_high ON friendships (user_high);
