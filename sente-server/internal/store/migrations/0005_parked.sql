-- Set when a node stops running a game on purpose (idle, drain, lost lease) and
-- cleared when a node picks it up again. A game found without it was stranded by a
-- crash, and the player on move is owed the gap; a game found with it simply sat
-- idle, as correspondence games do for days (docs/04 §4.5).
ALTER TABLE games ADD COLUMN parked_at TIMESTAMPTZ;
