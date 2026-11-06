-- The game's own purchase rules (openpak/prds#150). A game item can hand out several in-game keys (a DST chest gives
-- its skins) and can demand that the buyer already holds some (a chest with Wanda skins needs Wanda, as the game's
-- shop requires). `grants` lists the keys an owned item stands for; `requires` the keys the buyer must already hold,
-- each held through any owned item whose game_ref or grants contains it.
ALTER TABLE game_items ADD COLUMN grants   TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE game_items ADD COLUMN requires TEXT[] NOT NULL DEFAULT '{}';
