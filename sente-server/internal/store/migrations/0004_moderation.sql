-- Report and block (docs/05 §4, §11). Required by App Store guideline 1.2 for any
-- app with user-generated content; a display name counts.

CREATE TABLE blocks (
    blocker_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CONSTRAINT no_self_block CHECK (blocker_id <> blocked_id)
);

CREATE TABLE reports (
    id          UUID PRIMARY KEY,
    reporter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reported_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    game_id     UUID REFERENCES games(id) ON DELETE SET NULL,
    category    TEXT NOT NULL CHECK (category IN ('abuse','cheating','escaping','name','other')),
    note        TEXT CHECK (char_length(note) <= 1000),
    status      TEXT NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open','reviewing','actioned','dismissed')),
    resolution  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    CONSTRAINT no_self_report CHECK (reporter_id <> reported_id)
);

CREATE INDEX idx_reports_open ON reports (created_at) WHERE status IN ('open', 'reviewing');
CREATE INDEX idx_reports_target ON reports (reported_id, created_at DESC);
