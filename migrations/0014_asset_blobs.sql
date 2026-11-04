-- Reward artwork bytes live in the database, keyed by sha256 (item_assets holds the metadata).
CREATE TABLE IF NOT EXISTS asset_blobs (
    sha256 TEXT PRIMARY KEY,
    data   BYTEA NOT NULL,
    at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
