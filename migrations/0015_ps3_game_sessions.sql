-- Card #132: PlayStation 3 game sessions. Demonware (Call of Duty: Modern Warfare 3 on PS3) reports a host's
-- open game as a room under the adapter namespace `np` (subject = online id, which the core resolves through the
-- np link). Same shape as 0005: one event per session shared with another player, counted once per account.
INSERT INTO metrics (id, family, title, mode, daily_cap, max_delta, require_friend) VALUES
 ('game_sessions.call-of-duty-modern-warfare-3', 'game_sessions', 'Call of Duty: Modern Warfare 3 online sessions', 'distinct', 40, 1, false)
ON CONFLICT (id) DO NOTHING;
