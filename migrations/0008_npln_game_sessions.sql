-- Online sessions for the Switch NPLN titles (card #77). npln reports one event per player per game session
-- after 3 minutes with another player; the key is the session id. Same shape as 0005. Terraria shares its
-- id with the Wii U title, so it is a no-op here.
INSERT INTO metrics (id, family, title, mode, daily_cap, max_delta, require_friend) VALUES
 ('game_sessions.bayonetta-3', 'game_sessions', 'Bayonetta 3 online sessions', 'distinct', 40, 1, false),
 ('game_sessions.core-keeper', 'game_sessions', 'Core Keeper online sessions', 'distinct', 40, 1, false),
 ('game_sessions.dinkum', 'game_sessions', 'Dinkum online sessions', 'distinct', 40, 1, false),
 ('game_sessions.endless-ocean-luminous', 'game_sessions', 'Endless Ocean Luminous online sessions', 'distinct', 40, 1, false),
 ('game_sessions.go-go-town', 'game_sessions', 'Go-Go Town online sessions', 'distinct', 40, 1, false),
 ('game_sessions.hamster-playground', 'game_sessions', 'Hamster Playground online sessions', 'distinct', 40, 1, false),
 ('game_sessions.human-fall-flat', 'game_sessions', 'Human Fall Flat online sessions', 'distinct', 40, 1, false),
 ('game_sessions.metal-gear-solid-peace-walker', 'game_sessions', 'Metal Gear Solid Peace Walker online sessions', 'distinct', 40, 1, false),
 ('game_sessions.monster-hunter-rise', 'game_sessions', 'Monster Hunter Rise online sessions', 'distinct', 40, 1, false),
 ('game_sessions.plateup', 'game_sessions', 'PlateUp online sessions', 'distinct', 40, 1, false),
 ('game_sessions.pokemon-legends-arceus', 'game_sessions', 'Pokemon Legends Arceus online sessions', 'distinct', 40, 1, false),
 ('game_sessions.pokemon-scarlet-violet', 'game_sessions', 'Pokemon Scarlet Violet online sessions', 'distinct', 40, 1, false),
 ('game_sessions.stardew-valley', 'game_sessions', 'Stardew Valley online sessions', 'distinct', 40, 1, false),
 ('game_sessions.super-mario-bros-wonder', 'game_sessions', 'Super Mario Bros Wonder online sessions', 'distinct', 40, 1, false),
 ('game_sessions.super-mario-party-jamboree', 'game_sessions', 'Super Mario Party Jamboree online sessions', 'distinct', 40, 1, false),
 ('game_sessions.terraria', 'game_sessions', 'Terraria online sessions', 'distinct', 40, 1, false),
 ('game_sessions.wavecade', 'game_sessions', 'Wavecade online sessions', 'distinct', 40, 1, false)
ON CONFLICT (id) DO NOTHING;
