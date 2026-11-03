-- Online sessions for the third-party vendor hosts (card #78): Photon, Klei, Team17, EOS, Battle.net, Among Us,
-- Demonware, PlayFab, UGS, Shiro. One event per player per room after 3 minutes with another player; the key is
-- the room id. Same shape as 0005. Ids that already exist are a no-op. Spelunky 2 has no rooms: its server counts
-- the daily challenge (key: the day) and ranked runs (key: the run id) it receives results for.
INSERT INTO metrics (id, family, title, mode, daily_cap, max_delta, require_friend) VALUES
 ('game_sessions.outbound', 'game_sessions', 'Outbound online sessions', 'distinct', 40, 1, false),
 ('game_sessions.moving-out', 'game_sessions', 'Moving Out online sessions', 'distinct', 40, 1, false),
 ('game_sessions.moving-out-2', 'game_sessions', 'Moving Out 2 online sessions', 'distinct', 40, 1, false),
 ('game_sessions.we-were-here', 'game_sessions', 'We Were Here online sessions', 'distinct', 40, 1, false),
 ('game_sessions.stumble-guys', 'game_sessions', 'Stumble Guys online sessions', 'distinct', 40, 1, false),
 ('game_sessions.among-us', 'game_sessions', 'Among Us online sessions', 'distinct', 40, 1, false),
 ('game_sessions.fall-guys', 'game_sessions', 'Fall Guys online sessions', 'distinct', 40, 1, false),
 ('game_sessions.diablo-ii-resurrected', 'game_sessions', 'Diablo II Resurrected online sessions', 'distinct', 40, 1, false),
 ('game_sessions.diablo-3', 'game_sessions', 'Diablo III online sessions', 'distinct', 40, 1, false),
 ('game_sessions.crash-team-racing-nitro-fueled', 'game_sessions', 'Crash Team Racing Nitro-Fueled online sessions', 'distinct', 40, 1, false),
 ('game_sessions.doom-plus-doom-ii', 'game_sessions', 'DOOM + DOOM II online sessions', 'distinct', 40, 1, false),
 ('game_sessions.northgard', 'game_sessions', 'Northgard online sessions', 'distinct', 40, 1, false),
 ('game_sessions.tmnt-shredders-revenge', 'game_sessions', 'TMNT Shredder''s Revenge online sessions', 'distinct', 40, 1, false),
 ('game_sessions.plants-vs-zombies-battle-for-neighborville', 'game_sessions', 'Plants vs. Zombies Battle for Neighborville online sessions', 'distinct', 40, 1, false),
 ('game_sessions.brawlhalla', 'game_sessions', 'Brawlhalla online sessions', 'distinct', 40, 1, false),
 ('game_sessions.unrailed', 'game_sessions', 'Unrailed! online sessions', 'distinct', 40, 1, false),
 ('game_sessions.super-animal-royale', 'game_sessions', 'Super Animal Royale online sessions', 'distinct', 40, 1, false),
 ('game_sessions.grounded', 'game_sessions', 'Grounded online sessions', 'distinct', 40, 1, false),
 ('game_sessions.hello-kitty-island-adventure', 'game_sessions', 'Hello Kitty Island Adventure online sessions', 'distinct', 40, 1, false),
 ('game_sessions.black-ice', 'game_sessions', 'Black Ice online sessions', 'distinct', 40, 1, false),
 ('game_sessions.core-keeper', 'game_sessions', 'Core Keeper online sessions', 'distinct', 40, 1, false),
 ('game_sessions.dont-starve-together', 'game_sessions', 'Don''t Starve Together online sessions', 'distinct', 40, 1, false),
 ('game_sessions.gang-beasts', 'game_sessions', 'Gang Beasts online sessions', 'distinct', 40, 1, false),
 ('spelunky2_daily_runs', 'spelunky2', 'Spelunky 2 daily challenge runs submitted', 'distinct', 1, 1, false),
 ('spelunky2_ranked_runs', 'spelunky2', 'Spelunky 2 ranked runs submitted', 'distinct', 20, 1, false)
ON CONFLICT (id) DO NOTHING;
