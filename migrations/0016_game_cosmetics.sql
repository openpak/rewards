-- Game cosmetics (openpak/prds#141, prds/repos/rewards-game-cosmetics-plan.md).
-- A game cosmetic is an ordinary item (kind 'game_cosmetic') plus one game_items row that says which
-- game it belongs to and which in-game id it stands for. OpenPak hosts no art for it: licence 'game-native'.
ALTER TABLE items DROP CONSTRAINT IF EXISTS items_kind_check;
ALTER TABLE items ADD CONSTRAINT items_kind_check CHECK (kind IN ('badge', 'profile_icon', 'theme', 'title', 'switch_icon',
    'x360_gamerpic', 'x360_theme', 'ps3_avatar', 'ps3_theme', 'game_cosmetic'));
ALTER TABLE items DROP CONSTRAINT IF EXISTS items_license_check;
ALTER TABLE items ADD CONSTRAINT items_license_check CHECK (license IN ('openpak-original', 'game-native'));
ALTER TABLE redemptions DROP CONSTRAINT IF EXISTS redemptions_source_check;
ALTER TABLE redemptions ADD CONSTRAINT redemptions_source_check CHECK (source IN ('shop', 'mission', 'admin', 'event'));

CREATE TABLE game_items (
    item_id     TEXT PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    game_id     TEXT NOT NULL CHECK (game_id ~ '^[a-z0-9][a-z0-9-]{1,40}$'),
    game_ref    TEXT NOT NULL CHECK (game_ref <> '' AND length(game_ref) <= 120),
    game_slot   TEXT NOT NULL CHECK (game_slot ~ '^[a-z0-9_]{1,24}$'),
    rarity      TEXT NOT NULL DEFAULT 'common' CHECK (rarity IN ('common', 'rare', 'epic', 'legendary', 'event')),
    origin      TEXT NOT NULL DEFAULT 'earned' CHECK (origin IN ('base', 'earned', 'store', 'event', 'promo', 'openpak')),
    deliverable BOOLEAN NOT NULL DEFAULT false,
    verified_at TIMESTAMPTZ,
    catalog_rev TEXT NOT NULL DEFAULT '',
    UNIQUE (game_id, game_ref)
);
CREATE INDEX game_items_by_game ON game_items (game_id, game_slot);

-- What a player has in each game slot ("skin", "emote:1"). The game server writes it; Rewards checks ownership.
CREATE TABLE game_equipped (
    account_id TEXT NOT NULL,
    game_id    TEXT NOT NULL,
    slot       TEXT NOT NULL,
    item_id    TEXT NOT NULL REFERENCES items (id),
    PRIMARY KEY (account_id, game_id, slot)
);

-- Per account and game, a counter that moves on every grant, revoke or equip: adapters cache against it.
CREATE TABLE game_rev (
    account_id TEXT NOT NULL,
    game_id    TEXT NOT NULL,
    rev        BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, game_id)
);
