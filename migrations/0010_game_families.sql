-- Card #80: three new metric families so ladders can span a group of games. The members keep their ids
-- (game_sessions.<game>, the ids the servers already send); only the family column moves, so a mission
-- whose metric is the family name sums its members and family_games:<family> counts the games played.
--   sports_sessions  ARMS, Mario Tennis Aces, Mario Strikers: Battle League, Mario Golf: Super Rush
--   royale_sessions  Super Mario Bros. 35, Pac-Man 99
--   hunt_sessions    the Monster Hunter games on Wii U, 3DS and Switch
INSERT INTO metrics (id, family, title, mode, daily_cap, max_delta, require_friend) VALUES
 ('game_sessions.monster-hunter-generations-ultimate', 'hunt_sessions', 'Monster Hunter Generations Ultimate online sessions', 'distinct', 40, 1, false)
ON CONFLICT (id) DO NOTHING;
UPDATE metrics SET family='sports_sessions' WHERE id IN ('game_sessions.arms', 'game_sessions.mario-tennis-aces',
 'game_sessions.mario-strikers-battle-league', 'game_sessions.mario-golf-super-rush');
UPDATE metrics SET family='royale_sessions' WHERE id IN ('game_sessions.super-mario-bros-35', 'game_sessions.pac-man-99');
UPDATE metrics SET family='hunt_sessions' WHERE id IN ('game_sessions.monster-hunter-3-ultimate', 'game_sessions.monster-hunter-4',
 'game_sessions.monster-hunter-4-ultimate', 'game_sessions.monster-hunter-generations', 'game_sessions.monster-hunter-stories',
 'game_sessions.monster-hunter-xx', 'game_sessions.monster-hunter-generations-ultimate', 'game_sessions.monster-hunter-rise');
