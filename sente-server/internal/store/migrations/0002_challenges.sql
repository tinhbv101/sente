-- Invitations. A challenge is an offer to play under a named configuration; when
-- someone accepts, it becomes a game (docs/01 FR-M1..M3).

CREATE TABLE challenges (
    id            UUID PRIMARY KEY,
    -- Short, readable, and drawn from the same unambiguous alphabet as friend
    -- codes so it can be read out over the phone.
    code          TEXT NOT NULL UNIQUE,
    creator_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- NULL means an open link: whoever opens it first may accept.
    invitee_id    UUID REFERENCES users(id) ON DELETE CASCADE,
    config        JSONB NOT NULL,
    creator_color TEXT NOT NULL CHECK (creator_color IN ('black', 'white', 'random')),
    status        TEXT NOT NULL
                       CHECK (status IN ('pending', 'accepted', 'declined', 'expired', 'cancelled')),
    game_id       UUID REFERENCES games(id) ON DELETE SET NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at   TIMESTAMPTZ,

    CONSTRAINT code_fmt CHECK (code ~ '^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{8}$'),
    CONSTRAINT accepted_has_game CHECK (status <> 'accepted' OR game_id IS NOT NULL),
    CONSTRAINT nobody_invites_themselves CHECK (invitee_id IS NULL OR invitee_id <> creator_id)
);

CREATE INDEX idx_challenges_creator ON challenges (creator_id, created_at DESC);
CREATE INDEX idx_challenges_invitee ON challenges (invitee_id, status)
    WHERE invitee_id IS NOT NULL;
-- The sweeper that expires stale invitations reads only this.
CREATE INDEX idx_challenges_expiry ON challenges (expires_at) WHERE status = 'pending';
