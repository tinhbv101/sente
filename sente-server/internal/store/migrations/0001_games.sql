-- Core game storage. The moves table is the source of truth; everything on the
-- games row is derived and can be rebuilt by replaying it (docs/03 ADR-006).

CREATE TABLE users (
    id            UUID PRIMARY KEY,
    display_name  TEXT        NOT NULL,
    friend_code   TEXT        NOT NULL UNIQUE,
    is_guest      BOOLEAN     NOT NULL DEFAULT TRUE,
    locale        TEXT        NOT NULL DEFAULT 'vi',
    settings      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    banned_until  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT display_name_len CHECK (char_length(display_name) BETWEEN 2 AND 24),
    CONSTRAINT friend_code_fmt  CHECK (friend_code ~ '^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{8}$')
);

CREATE TABLE games (
    id                 UUID PRIMARY KEY,
    board_size         SMALLINT     NOT NULL CHECK (board_size IN (9, 13, 19)),
    rules              TEXT         NOT NULL CHECK (rules IN ('japanese', 'chinese')),
    rules_version      TEXT         NOT NULL,
    komi               NUMERIC(4,1) NOT NULL,
    handicap           SMALLINT     NOT NULL DEFAULT 0
                                    CHECK (handicap = 0 OR handicap BETWEEN 2 AND 9),
    time_control       JSONB        NOT NULL,
    is_ranked          BOOLEAN      NOT NULL DEFAULT FALSE,
    is_correspondence  BOOLEAN      NOT NULL,

    black_user_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    white_user_id      UUID REFERENCES users(id) ON DELETE SET NULL,

    phase              TEXT     NOT NULL
                                CHECK (phase IN ('pending','playing','scoring','finished','aborted')),
    to_play            TEXT     CHECK (to_play IN ('black', 'white')),
    current_move_no    INT      NOT NULL DEFAULT 0,
    consecutive_passes SMALLINT NOT NULL DEFAULT 0,
    ko_point           SMALLINT,
    -- Zobrist hash of the current position, stored as a checksum: a rebuild that
    -- disagrees with it means the engine changed, and must be shouted about.
    board_hash         BIGINT   NOT NULL,
    captures_black     SMALLINT NOT NULL DEFAULT 0,
    captures_white     SMALLINT NOT NULL DEFAULT 0,

    clock              JSONB       NOT NULL,
    move_deadline      TIMESTAMPTZ,
    result             JSONB,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at         TIMESTAMPTZ,
    ended_at           TIMESTAMPTZ,
    last_activity_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT finished_has_result  CHECK (phase <> 'finished' OR result IS NOT NULL),
    CONSTRAINT playing_has_deadline CHECK (phase <> 'playing' OR move_deadline IS NOT NULL)
);

CREATE INDEX idx_games_black_active ON games (black_user_id, last_activity_at DESC)
    WHERE phase IN ('playing', 'scoring');
CREATE INDEX idx_games_white_active ON games (white_user_id, last_activity_at DESC)
    WHERE phase IN ('playing', 'scoring');
CREATE INDEX idx_games_deadline ON games (move_deadline)
    WHERE phase = 'playing' AND is_correspondence;
CREATE INDEX idx_games_stale ON games (last_activity_at)
    WHERE phase IN ('playing', 'scoring');

CREATE TABLE moves (
    game_id        UUID        NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    move_no        INT         NOT NULL,
    color          TEXT        NOT NULL CHECK (color IN ('black', 'white')),
    kind           TEXT        NOT NULL CHECK (kind IN ('play', 'pass', 'resign')),
    point          SMALLINT,
    captured_count SMALLINT    NOT NULL DEFAULT 0,
    board_hash     BIGINT      NOT NULL,
    client_move_id UUID,
    played_at      TIMESTAMPTZ NOT NULL,
    time_left_ms   INT,
    periods_left   SMALLINT,

    PRIMARY KEY (game_id, move_no),
    CONSTRAINT play_has_point CHECK ((kind = 'play') = (point IS NOT NULL))
);

-- Stops a move resent after a reconnect from being written twice (docs/03 ADR-007).
CREATE UNIQUE INDEX idx_moves_idempotency ON moves (game_id, client_move_id)
    WHERE client_move_id IS NOT NULL;

CREATE TABLE game_scoring (
    game_id             UUID PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
    dead_points         SMALLINT[]  NOT NULL DEFAULT '{}',
    suggested_points    SMALLINT[]  NOT NULL DEFAULT '{}',
    black_accepted      BOOLEAN     NOT NULL DEFAULT FALSE,
    white_accepted      BOOLEAN     NOT NULL DEFAULT FALSE,
    computed_score      JSONB,
    resume_from_move_no INT         NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only audit of everything that is not a move. This is what answers
-- "why did this game end like that?" when a player complains.
CREATE TABLE game_events (
    id            BIGSERIAL PRIMARY KEY,
    game_id       UUID        NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    move_no       INT         NOT NULL,
    actor_user_id UUID        REFERENCES users(id) ON DELETE SET NULL,
    kind          TEXT        NOT NULL,
    payload       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_game_events_game ON game_events (game_id, id);
