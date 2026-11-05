-- Changes to a player's game cosmetics waiting to be announced on the account core stream as
-- entitlement_changed (openpak/prds#141). Written in the same transaction as the change, drained by the service.
CREATE TABLE game_outbox (
    id         BIGSERIAL PRIMARY KEY,
    account_id TEXT NOT NULL,
    game_id    TEXT NOT NULL,
    at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX game_outbox_by_account ON game_outbox (account_id);
