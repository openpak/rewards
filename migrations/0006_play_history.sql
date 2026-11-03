-- Play from before Rewards first saw an account counts once (owner, 2026-11-03): the uncounted part of
-- each title's total is the history. It feeds lifetime play, the titles played and one "history" payment,
-- but never the per-day activity, so play days and the daily/weekly missions are untouched.
ALTER TABLE play_cursor ADD COLUMN history_seconds BIGINT NOT NULL DEFAULT 0;
ALTER TABLE play_cursor ADD COLUMN history_done BOOLEAN NOT NULL DEFAULT true;
UPDATE play_cursor SET history_seconds = GREATEST(total_seconds - counted_seconds, 0), history_done = false;
ALTER TABLE play_cursor ALTER COLUMN history_done SET DEFAULT false;
