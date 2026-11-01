-- OpenPak rewards (docs/design.md section 7). Every account id is the account core's UUID
-- as lower-case text; no names, emails or birthdays live here.

CREATE TABLE accounts (
    account_id            TEXT PRIMARY KEY,
    balance               BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    lifetime_earned       BIGINT NOT NULL DEFAULT 0,
    lifetime_play_seconds BIGINT NOT NULL DEFAULT 0,
    frozen                BOOLEAN NOT NULL DEFAULT false,
    paused                BOOLEAN NOT NULL DEFAULT false,
    showcase_public       BOOLEAN NOT NULL DEFAULT false,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_poll_at          TIMESTAMPTZ
);

-- Append-only. idem_key makes every cause happen once.
CREATE TABLE ledger (
    id         BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL,
    delta      BIGINT NOT NULL CHECK (delta <> 0),
    kind       TEXT NOT NULL CHECK (kind IN ('earn', 'spend', 'adjust', 'reverse')),
    source     TEXT NOT NULL,
    ref        TEXT NOT NULL DEFAULT '',
    idem_key   TEXT NOT NULL UNIQUE,
    actor      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ledger_by_account ON ledger (account_id, id DESC);

-- One row per (account, namespace, title): where the account core's total was last seen.
CREATE TABLE play_cursor (
    account_id      TEXT NOT NULL,
    namespace       TEXT NOT NULL,
    title_id        TEXT NOT NULL,
    total_seconds   BIGINT NOT NULL,
    sessions        INT NOT NULL DEFAULT 0,
    counted_seconds BIGINT NOT NULL DEFAULT 0,
    carry_seconds   BIGINT NOT NULL DEFAULT 0,
    seen_at         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account_id, namespace, title_id)
);

-- The cap counters. bucket is the title for 'play', empty for the other sources.
CREATE TABLE daily_earn (
    account_id TEXT NOT NULL,
    day        DATE NOT NULL,
    source     TEXT NOT NULL,
    bucket     TEXT NOT NULL DEFAULT '',
    points     INT NOT NULL DEFAULT 0,
    seconds    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, day, source, bucket)
);

-- Counted play per day, network and title: the source of every mission metric.
CREATE TABLE activity (
    account_id TEXT NOT NULL,
    day        DATE NOT NULL,
    network    TEXT NOT NULL,
    title_id   TEXT NOT NULL,
    seconds    BIGINT NOT NULL DEFAULT 0,
    sessions   INT NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, day, network, title_id)
);

CREATE TABLE titles_credited (
    account_id TEXT NOT NULL,
    title_id   TEXT NOT NULL,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, title_id)
);

CREATE TABLE friends (
    account_id TEXT NOT NULL,
    other_id   TEXT NOT NULL,
    awarded    BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, other_id)
);

CREATE TABLE missions (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL CHECK (kind IN ('daily', 'weekly', 'once', 'series')),
    metric        TEXT NOT NULL CHECK (metric IN ('play_seconds', 'play_days', 'titles_played', 'networks_played',
                                                  'friends_added', 'sessions', 'lifetime_play_seconds')),
    target        BIGINT NOT NULL CHECK (target > 0),
    network       TEXT NOT NULL DEFAULT '',
    title_id      TEXT NOT NULL DEFAULT '',
    requires      TEXT REFERENCES missions (id),
    reward_points INT NOT NULL DEFAULT 0 CHECK (reward_points >= 0),
    reward_item   TEXT,
    starts_at     TIMESTAMPTZ,
    ends_at       TIMESTAMPTZ,
    active        BOOLEAN NOT NULL DEFAULT true,
    sort          INT NOT NULL DEFAULT 100
);

CREATE TABLE mission_progress (
    account_id   TEXT NOT NULL,
    mission_id   TEXT NOT NULL REFERENCES missions (id),
    period       TEXT NOT NULL,
    progress     BIGINT NOT NULL DEFAULT 0,
    completed_at TIMESTAMPTZ,
    claimed_at   TIMESTAMPTZ,
    PRIMARY KEY (account_id, mission_id, period)
);

CREATE TABLE items (
    id                TEXT PRIMARY KEY,
    kind              TEXT NOT NULL CHECK (kind IN ('badge', 'profile_icon', 'theme', 'title', 'switch_icon',
                                                    'x360_gamerpic', 'x360_theme', 'ps3_avatar', 'ps3_theme')),
    name              TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    price             INT CHECK (price IS NULL OR price >= 0),
    platforms         TEXT[] NOT NULL DEFAULT '{}',
    stock             INT CHECK (stock IS NULL OR stock >= 0),
    available_from    TIMESTAMPTZ,
    available_until   TIMESTAMPTZ,
    limit_per_account INT NOT NULL DEFAULT 1,
    license           TEXT NOT NULL DEFAULT 'openpak-original' CHECK (license = 'openpak-original'),
    state             TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'active', 'retired')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE item_assets (
    item_id      TEXT NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    platform     TEXT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('asset', 'preview')),
    sha256       TEXT NOT NULL,
    bytes        BIGINT NOT NULL,
    content_type TEXT NOT NULL,
    PRIMARY KEY (item_id, platform, role)
);

CREATE TABLE redemptions (
    id         BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL,
    item_id    TEXT NOT NULL REFERENCES items (id),
    price      INT NOT NULL,
    source     TEXT NOT NULL CHECK (source IN ('shop', 'mission', 'admin')),
    state      TEXT NOT NULL DEFAULT 'granted' CHECK (state IN ('granted', 'revoked')),
    idem_key   TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX redemptions_by_account ON redemptions (account_id, id DESC);
CREATE INDEX redemptions_by_item ON redemptions (item_id);

CREATE TABLE equipped (
    account_id TEXT NOT NULL,
    slot       TEXT NOT NULL,
    item_id    TEXT NOT NULL REFERENCES items (id),
    PRIMARY KEY (account_id, slot)
);

CREATE TABLE flags (
    id          BIGSERIAL PRIMARY KEY,
    account_id  TEXT NOT NULL,
    reason      TEXT NOT NULL,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    raised_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_by TEXT,
    resolved_at TIMESTAMPTZ
);
CREATE INDEX flags_open ON flags (raised_at) WHERE resolved_at IS NULL;
-- One open flag per account and reason.
CREATE UNIQUE INDEX flags_one_open ON flags (account_id, reason) WHERE resolved_at IS NULL;

CREATE TABLE audit (
    id     BIGSERIAL PRIMARY KEY,
    at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor  TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    detail JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE core_cursor (
    id            INT PRIMARY KEY CHECK (id = 1),
    since_version BIGINT NOT NULL
);
INSERT INTO core_cursor (id, since_version) VALUES (1, 0);

INSERT INTO missions (id, title, description, kind, metric, target, reward_points, sort, requires) VALUES
 ('daily-play-30',    'Play for 30 minutes',        'Play online for 30 minutes today.',                 'daily',  'play_seconds',    1800,   2, 10, NULL),
 ('weekly-4-days',    'Four days of play',          'Play online on four different days this week.',     'weekly', 'play_days',       4,      5, 20, NULL),
 ('weekly-3-titles',  'Three different games',      'Play three different games online this week.',      'weekly', 'titles_played',   3,      5, 21, NULL),
 ('weekly-2-networks','Two networks',               'Play on two different networks this week.',         'weekly', 'networks_played', 2,      5, 22, NULL),
 ('first-online',     'First steps online',         'Play online for the first time.',                   'once',   'play_seconds',    60,     3, 30, NULL),
 ('first-friend',     'Make a friend',              'Add your first friend.',                            'once',   'friends_added',   1,      5, 31, NULL),
 ('hours-10',         '10 hours online',            'Play online for 10 hours in total.',                'once',   'lifetime_play_seconds', 36000, 20, 40, NULL),
 ('all-networks',     'Three networks',             'Play on three different networks.',                 'once',   'networks_played', 3,     30, 42, NULL);
INSERT INTO missions (id, title, description, kind, metric, target, reward_points, sort, requires) VALUES
 ('hours-50',         '50 hours online',            'Play online for 50 hours in total.',                'series', 'lifetime_play_seconds', 180000, 50, 41, 'hours-10');
