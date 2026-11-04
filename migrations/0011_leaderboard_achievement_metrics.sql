-- Card #67: metrics for the core leaderboards and achievements services. Both announce one event per
-- fact with a stable id (lb:<title>:<board>:<account>:<ms> and ach:<title>:<achievement>:<account>), so a
-- retry never counts twice, and send no keys: mode 'sum', delta 1.
--   leaderboard_new_best.<title>   a first entry or a better score on any board of the title
--   achievement_unlocked.<title>   a trophy or achievement unlocked (first sight of it, not a re-sync)
-- <title> is the neutral title key the service files the data under. Rewards rejects metrics it does not
-- know, so add a row here for each title as it starts emitting (one row per title and type).
-- Families carry the type, so a mission whose metric is the family name sums every title.
-- Caps are loose on purpose: a first sync of a library unlocks many trophies at once, and a held event
-- raises an operator flag.
INSERT INTO metrics (id, family, title, mode, daily_cap, max_delta, require_friend) VALUES
 ('leaderboard_new_best.terraria',            'leaderboard_new_best', 'Terraria new personal bests',                 'sum', 100, 1, false),
 ('leaderboard_new_best.mario-kart-8-deluxe', 'leaderboard_new_best', 'Mario Kart 8 Deluxe new personal bests',      'sum', 100, 1, false),
 ('achievement_unlocked.terraria',            'achievement_unlocked', 'Terraria achievements unlocked',              'sum', 200, 1, false)
ON CONFLICT (id) DO NOTHING;
