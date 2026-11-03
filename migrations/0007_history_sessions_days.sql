-- Play from before Rewards also counts its online sessions and its play days (owner, 2026-11-03).
-- history_sessions: the cursor's session count that no activity row accounts for yet.
ALTER TABLE play_cursor ADD COLUMN history_sessions BIGINT NOT NULL DEFAULT 0;
UPDATE play_cursor c SET history_sessions = GREATEST(c.sessions - COALESCE(
    (SELECT SUM(a.sessions) FROM activity a WHERE a.account_id = c.account_id AND a.title_id = c.title_id), 0), 0);

-- history_days: UTC days played before the account's first sight, from the account core. Separate from
-- activity on purpose: no daily or weekly period query reads it.
CREATE TABLE history_days (
    account_id TEXT NOT NULL,
    day        DATE NOT NULL,
    PRIMARY KEY (account_id, day)
);
ALTER TABLE accounts ADD COLUMN history_days_done BOOLEAN NOT NULL DEFAULT false;
