-- Kart, Turf and Brawl badges count online sessions per game (a lobby the player stayed in for
-- a few minutes with another player), not finished races. The "completed" metrics go away.
DELETE FROM metric_keys WHERE metric LIKE 'kart_races_completed.%' OR metric LIKE 'splatoon_matches_completed.%' OR metric LIKE 'smash_battles_completed.%';
DELETE FROM metric_counts WHERE metric LIKE 'kart_races_completed.%' OR metric LIKE 'splatoon_matches_completed.%' OR metric LIKE 'smash_battles_completed.%';
DELETE FROM metric_daily WHERE metric LIKE 'kart_races_completed.%' OR metric LIKE 'splatoon_matches_completed.%' OR metric LIKE 'smash_battles_completed.%';
DELETE FROM events WHERE metric LIKE 'kart_races_completed.%' OR metric LIKE 'splatoon_matches_completed.%' OR metric LIKE 'smash_battles_completed.%';
DELETE FROM metrics WHERE id LIKE 'kart_races_completed.%' OR id LIKE 'splatoon_matches_completed.%' OR id LIKE 'smash_battles_completed.%';

-- One event per session, key = the session (gathering) id, so a session counts once per account.
INSERT INTO metrics (id, family, title, mode, daily_cap, max_delta, require_friend) VALUES
 ('kart_sessions.mkds',  'kart_sessions', 'Mario Kart DS online sessions',          'distinct', 40, 1, false),
 ('kart_sessions.mkwii', 'kart_sessions', 'Mario Kart Wii online sessions',        'distinct', 40, 1, false),
 ('kart_sessions.mk7',   'kart_sessions', 'Mario Kart 7 online sessions',          'distinct', 40, 1, false),
 ('kart_sessions.mk8',   'kart_sessions', 'Mario Kart 8 online sessions',          'distinct', 40, 1, false),
 ('kart_sessions.mk8dx', 'kart_sessions', 'Mario Kart 8 Deluxe online sessions',   'distinct', 40, 1, false),
 ('turf_sessions.s1',    'turf_sessions', 'Splatoon online sessions',              'distinct', 40, 1, false),
 ('turf_sessions.s2',    'turf_sessions', 'Splatoon 2 online sessions',            'distinct', 40, 1, false),
 ('brawl_sessions.3ds',  'brawl_sessions','Smash 3DS online sessions',             'distinct', 40, 1, false),
 ('brawl_sessions.wiiu', 'brawl_sessions','Smash Wii U online sessions',           'distinct', 40, 1, false),
 ('brawl_sessions.ult',  'brawl_sessions','Smash Ultimate online sessions',        'distinct', 40, 1, false);

-- Ladders keep their ids; they now count sessions.
UPDATE missions SET metric='kart_sessions',  description=target::text || ' Mario Kart online sessions, in any game.' WHERE id LIKE 'kart-races-%';
UPDATE missions SET metric='turf_sessions',  description=target::text || ' Splatoon online sessions, in any game.' WHERE id LIKE 'splatoon-matches-%';
UPDATE missions SET metric='brawl_sessions', description=target::text || ' Smash online sessions, in any game.' WHERE id LIKE 'smash-battles-%';
UPDATE missions SET title='Kart Sessions: ' || to_char(target, 'FM999,999') WHERE id LIKE 'kart-races-%';
UPDATE missions SET title='Turf Sessions: ' || to_char(target, 'FM999,999') WHERE id LIKE 'splatoon-matches-%';
UPDATE missions SET title='Brawl Sessions: ' || to_char(target, 'FM999,999') WHERE id LIKE 'smash-battles-%';

-- Per-game firsts.
UPDATE missions SET metric='kart_sessions.'  || substr(id, 12), title='First online session: ' || replace(title,'First finish: ',''), description='Play an online session of ' || replace(title,'First finish: ','') || '.' WHERE id LIKE 'kart-first-%';
UPDATE missions SET metric='turf_sessions.'  || substr(id, 16), title='First online session: ' || replace(title,'First finish: ',''), description='Play an online session of ' || replace(title,'First finish: ','') || '.' WHERE id LIKE 'splatoon-first-%';
UPDATE missions SET metric='brawl_sessions.' || substr(id, 13), title='First online session: ' || replace(title,'First finish: ',''), description='Play an online session of ' || replace(title,'First finish: ','') || '.' WHERE id LIKE 'smash-first-%';

-- Splatoon 3 is not hosted for matchmaking: no metric, no first-session mission, a two-game Collector.
DELETE FROM missions WHERE id = 'splatoon-first-s3';
UPDATE missions SET metric='family_games:kart_sessions',  description='Play an online session in each of: Mario Kart DS, Mario Kart Wii, Mario Kart 7, Mario Kart 8, Mario Kart 8 Deluxe.' WHERE id='kart-collector';
UPDATE missions SET metric='family_games:turf_sessions',  target=2, description='Play an online session in both Splatoon and Splatoon 2.' WHERE id='splatoon-collector';
UPDATE missions SET metric='family_games:brawl_sessions', description='Play an online session in each of: Smash 3DS, Smash Wii U, Smash Ultimate.' WHERE id='smash-collector';
