-- Splatoon 3 online sessions: npln reports one event per player per game session (3 minutes together).
INSERT INTO metrics (id, family, title, mode, daily_cap, max_delta, require_friend) VALUES
 ('turf_sessions.s3', 'turf_sessions', 'Splatoon 3 online sessions', 'distinct', 40, 1, false)
ON CONFLICT (id) DO NOTHING;
