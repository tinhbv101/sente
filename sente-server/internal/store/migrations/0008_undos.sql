-- Undo count per game (FR-G10 cap). Was never stored, so a reloaded game forgot
-- how many undos had been spent.
ALTER TABLE games ADD COLUMN undos_used SMALLINT NOT NULL DEFAULT 0;
